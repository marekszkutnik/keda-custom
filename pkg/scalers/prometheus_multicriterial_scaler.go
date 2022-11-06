package scalers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	url_pkg "net/url"
	"strconv"
	"time"

	"github.com/go-logr/logr"
	v2 "k8s.io/api/autoscaling/v2"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/metrics/pkg/apis/external_metrics"

	"github.com/kedacore/keda/v2/pkg/scalers/authentication"
	kedautil "github.com/kedacore/keda/v2/pkg/util"
)

const (
	promMulticriterialServerAddress       = "serverAddress"
	promMulticriterialMetricName          = "metricName"
	promQueryParam1                       = "queryParam1"
	promQuery1                            = "query1"
	promQueryParam2                       = "queryParam2"
	promQuery2                            = "query2"
	promFullQuery                         = "fullQuery"
	promThreshold1                        = "threshold1"
	promThreshold2                        = "threshold2"
	promTotalThreshold                    = "totalThreshold"
	promMulticriterialActivationThreshold = "activationThreshold"
	promMulticriterialNamespace           = "namespace"
	promMulticriterialCortexScopeOrgID    = "cortexOrgID"
	promMulticriterialCortexHeaderKey     = "X-Scope-OrgID"
	promMulticriterialIgnoreNullValues    = "ignoreNullValues"
	promMulticriterialunsafeSsl           = "unsafeSsl"
)

var (
	promMulticriterialdefaultIgnoreNullValues = true
)

type prometheusMulticriterialScaler struct {
	metricType v2.MetricTargetType
	metadata   *prometheusMulticriterialMetadata
	httpClient *http.Client
	logger     logr.Logger
}

type prometheusMulticriterialMetadata struct {
	serverAddress       string
	metricName          string
	queryParam1         float64
	query1              string
	queryParam2         float64
	query2              string
	fullQuery           string
	threshold1          float64
	threshold2          float64
	totalThreshold      float64
	activationThreshold float64
	prometheusAuth      *authentication.AuthMeta
	namespace           string
	scalerIndex         int
	cortexOrgID         string
	// sometimes should consider there is an error we can accept
	// default value is true/t, to ignore the null value return from prometheus
	// change to false/f if can not accept prometheus return null values
	// https://github.com/kedacore/keda/issues/3065
	ignoreNullValues bool
	unsafeSsl        bool
}

type promMulticriterialQueryResult struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric struct {
			} `json:"metric"`
			Value []interface{} `json:"value"`
		} `json:"result"`
	} `json:"data"`
}

// NewPrometheusMulticriterialScaler creates a new prometheusScaler
func NewPrometheusMulticriterialScaler(config *ScalerConfig) (Scaler, error) {
	metricType, err := GetMetricTargetType(config)
	if err != nil {
		return nil, fmt.Errorf("error getting scaler metric type: %s", err)
	}

	logger := InitializeLogger(config, "prometheus_scaler")

	meta, err := parsePrometheusMulticriterialMetadata(config)
	if err != nil {
		return nil, fmt.Errorf("error parsing prometheus metadata: %s", err)
	}

	httpClient := kedautil.CreateHTTPClient(config.GlobalHTTPTimeout, meta.unsafeSsl)

	if meta.prometheusAuth != nil && (meta.prometheusAuth.CA != "" || meta.prometheusAuth.EnableTLS) {
		// create http.RoundTripper with auth settings from ScalerConfig
		if httpClient.Transport, err = authentication.CreateHTTPRoundTripper(
			authentication.NetHTTP,
			meta.prometheusAuth,
		); err != nil {
			logger.V(1).Error(err, "init Prometheus client http transport")
			return nil, err
		}
	}

	return &prometheusScaler{
		metricType: metricType,
		metadata:   meta,
		httpClient: httpClient,
		logger:     logger,
	}, nil
}

func parsePrometheusMulticriterialMetadata(config *ScalerConfig) (meta *prometheusMulticriterialMetadata, err error) {
	meta = &prometheusMulticriterialMetadata{}

	if val, ok := config.TriggerMetadata[promMulticriterialServerAddress]; ok && val != "" {
		meta.serverAddress = val
	} else {
		return nil, fmt.Errorf("no %s given", promMulticriterialServerAddress)
	}

	if val, ok := config.TriggerMetadata[promQueryParam1]; ok && val != "" {
		t, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return nil, fmt.Errorf("error parsing %s: %s", promQueryParam1, err)
		}

		meta.queryParam1 = t
	} else {
		return nil, fmt.Errorf("no %s given", promQueryParam1)
	}

	if val, ok := config.TriggerMetadata[promQuery1]; ok && val != "" {
		meta.query1 = val
	} else {
		return nil, fmt.Errorf("no %s given", promQuery1)
	}

	if val, ok := config.TriggerMetadata[promQueryParam2]; ok && val != "" {
		t, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return nil, fmt.Errorf("error parsing %s: %s", promQueryParam2, err)
		}

		meta.queryParam2 = t
	} else {
		return nil, fmt.Errorf("no %s given", promQueryParam2)
	}

	if val, ok := config.TriggerMetadata[promQuery2]; ok && val != "" {
		meta.query2 = val
	} else {
		return nil, fmt.Errorf("no %s given", promQuery2)
	}

	if val, ok := config.TriggerMetadata[promMulticriterialMetricName]; ok && val != "" {
		meta.metricName = val
	} else {
		return nil, fmt.Errorf("no %s given", promMulticriterialMetricName)
	}

	if val, ok := config.TriggerMetadata[promThreshold1]; ok && val != "" {
		t, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return nil, fmt.Errorf("error parsing %s: %s", promThreshold1, err)
		}

		meta.threshold1 = t
	} else {
		return nil, fmt.Errorf("no %s given", promThreshold1)
	}

	if val, ok := config.TriggerMetadata[promThreshold2]; ok && val != "" {
		t, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return nil, fmt.Errorf("error parsing %s: %s", promThreshold2, err)
		}

		meta.threshold2 = t
	} else {
		return nil, fmt.Errorf("no %s given", promThreshold2)
	}

	meta.activationThreshold = 0
	if val, ok := config.TriggerMetadata[promMulticriterialActivationThreshold]; ok {
		t, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return nil, fmt.Errorf("activationThreshold parsing error %s", err.Error())
		}

		meta.activationThreshold = t
	}

	if val, ok := config.TriggerMetadata[promMulticriterialNamespace]; ok && val != "" {
		meta.namespace = val
	}

	if val, ok := config.TriggerMetadata[promMulticriterialCortexScopeOrgID]; ok && val != "" {
		meta.cortexOrgID = val
	}

	meta.ignoreNullValues = promMulticriterialdefaultIgnoreNullValues
	if val, ok := config.TriggerMetadata[promMulticriterialIgnoreNullValues]; ok && val != "" {
		ignoreNullValues, err := strconv.ParseBool(val)
		if err != nil {
			return nil, fmt.Errorf("err incorrect value for ignoreNullValues given: %s, "+
				"please use true or false", val)
		}
		meta.ignoreNullValues = ignoreNullValues
	}

	meta.unsafeSsl = false
	if val, ok := config.TriggerMetadata[promMulticriterialunsafeSsl]; ok && val != "" {
		unsafeSslValue, err := strconv.ParseBool(val)
		if err != nil {
			return nil, fmt.Errorf("error parsing %s: %s", unsafeSsl, err)
		}

		meta.unsafeSsl = unsafeSslValue
	}

	meta.scalerIndex = config.ScalerIndex

	// parse auth configs from ScalerConfig
	meta.prometheusAuth, err = authentication.GetAuthConfigs(config.TriggerMetadata, config.AuthParams)
	if err != nil {
		return nil, err
	}

	return meta, nil
}

func (s *prometheusMulticriterialScaler) IsActive(ctx context.Context) (bool, error) {
	val, err := s.ExecutePromQuery(ctx)
	if err != nil {
		s.logger.Error(err, "error executing prometheus query")
		return false, err
	}

	return val > s.metadata.activationThreshold, nil
}

func (s *prometheusMulticriterialScaler) Close(context.Context) error {
	return nil
}

func (s *prometheusMulticriterialScaler) GetMetricSpecForScaling(context.Context) []v2.MetricSpec {
	metricName := kedautil.NormalizeString(fmt.Sprintf("prometheus-multicriterial-%s", s.metadata.metricName))

	totalThreshold := s.metadata.queryParam1*s.metadata.threshold1 + s.metadata.queryParam2*s.metadata.threshold2

	externalMetric := &v2.ExternalMetricSource{
		Metric: v2.MetricIdentifier{
			Name: GenerateMetricNameWithIndex(s.metadata.scalerIndex, metricName),
		},
		Target: GetMetricTargetMili(s.metricType, totalThreshold),
	}
	metricSpec := v2.MetricSpec{
		External: externalMetric, Type: externalMetricType,
	}
	return []v2.MetricSpec{metricSpec}
}

func (s *prometheusMulticriterialScaler) ExecutePromQuery(ctx context.Context) (float64, error) {
	t := time.Now().UTC().Format(time.RFC3339)

	strQueryParam1 := strconv.FormatFloat(s.metadata.queryParam1, 'E', -1, 64)
	strQueryParam2 := strconv.FormatFloat(s.metadata.queryParam2, 'E', -1, 64)

	// fullQuery = "(" + s.metadata.queryParam1 + "*" + s.metadata.query1 + "+" + s.metadata.queryParam2 + "*" + s.metadata.query2 + ")" + " / " + "(" + s.metadata.queryParam1 + "+" + s.metadata.queryParam1 + ")"
	fullQuery := strQueryParam1 + "*" + s.metadata.query1 + "+" + strQueryParam2 + "*" + s.metadata.query2

	queryEscaped := url_pkg.QueryEscape(fullQuery)
	url := fmt.Sprintf("%s/api/v1/query?query=%s&time=%s", s.metadata.serverAddress, queryEscaped, t)

	// set 'namespace' parameter for namespaced Prometheus requests (eg. for Thanos Querier)
	if s.metadata.namespace != "" {
		url = fmt.Sprintf("%s&namespace=%s", url, s.metadata.namespace)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return -1, err
	}

	if s.metadata.prometheusAuth != nil && s.metadata.prometheusAuth.EnableBearerAuth {
		req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", s.metadata.prometheusAuth.BearerToken))
	} else if s.metadata.prometheusAuth != nil && s.metadata.prometheusAuth.EnableBasicAuth {
		req.SetBasicAuth(s.metadata.prometheusAuth.Username, s.metadata.prometheusAuth.Password)
	}

	if s.metadata.cortexOrgID != "" {
		req.Header.Add(promMulticriterialCortexHeaderKey, s.metadata.cortexOrgID)
	}

	r, err := s.httpClient.Do(req)
	if err != nil {
		return -1, err
	}

	b, err := io.ReadAll(r.Body)
	if err != nil {
		return -1, err
	}
	_ = r.Body.Close()

	if !(r.StatusCode >= 200 && r.StatusCode <= 299) {
		err := fmt.Errorf("prometheus query api returned error. status: %d response: %s", r.StatusCode, string(b))
		s.logger.Error(err, "prometheus query api returned error")
		return -1, err
	}

	var result promMulticriterialQueryResult
	err = json.Unmarshal(b, &result)
	if err != nil {
		return -1, err
	}

	var v float64 = -1

	// allow for zero element or single element result sets
	if len(result.Data.Result) == 0 {
		if s.metadata.ignoreNullValues {
			return 0, nil
		}
		return -1, fmt.Errorf("prometheus metrics %s target may be lost, the result is empty", s.metadata.metricName)
	} else if len(result.Data.Result) > 1 {
		return -1, fmt.Errorf("prometheus query %s returned multiple elements", fullQuery)
	}

	valueLen := len(result.Data.Result[0].Value)
	if valueLen == 0 {
		if s.metadata.ignoreNullValues {
			return 0, nil
		}
		return -1, fmt.Errorf("prometheus metrics %s target may be lost, the value list is empty", s.metadata.metricName)
	} else if valueLen < 2 {
		return -1, fmt.Errorf("prometheus query %s didn't return enough values", fullQuery)
	}

	val := result.Data.Result[0].Value[1]
	if val != nil {
		str := val.(string)
		v, err = strconv.ParseFloat(str, 64)
		if err != nil {
			s.logger.Error(err, "Error converting prometheus value", "prometheus_value", str)
			return -1, err
		}
	}

	return v, nil
}

func (s *prometheusMulticriterialScaler) GetMetrics(ctx context.Context, metricName string, _ labels.Selector) ([]external_metrics.ExternalMetricValue, error) {
	val, err := s.ExecutePromQuery(ctx)
	if err != nil {
		s.logger.Error(err, "error executing prometheus query")
		return []external_metrics.ExternalMetricValue{}, err
	}

	metric := GenerateMetricInMili(metricName, val)

	return append([]external_metrics.ExternalMetricValue{}, metric), nil
}
