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
	// goclient
	// "bufio"
	// "context"
	// "fmt"
	// "os"
	// appsv1 "k8s.io/api/apps/v1"
	// "k8s.io/client-go/util/retry"
)

const (
	promMulticriterialRalServerAddress       = "serverAddress"
	promMulticriterialRalMetricName          = "metricName"
	promMulticriterialQuery1                 = "query1"
	promMulticriterialQuery2                 = "query2"
	promMulticriterialQuery3                 = "query3"
	promMulticriterialQuery4                 = "query4"
	promMulticriterialRalActivationThreshold = "activationThreshold"
	promMulticriterialRalNamespace           = "namespace"
	promMulticriterialRalCortexScopeOrgID    = "cortexOrgID"
	promMulticriterialRalCortexHeaderKey     = "X-Scope-OrgID"
	promMulticriterialRalIgnoreNullValues    = "ignoreNullValues"
	promMulticriterialRalunsafeSsl           = "unsafeSsl"
)

var (
	promMulticriterialRaldefaultIgnoreNullValues = true

	// lastReplicasCount int32 = 1
	defaultHPAMinReplicas float64 = 2.0
	defaultHPAMaxReplicas float64 = 5.0
)

type prometheusMulticriterialRalScaler struct {
	metricType v2.MetricTargetType
	metadata   *prometheusMulticriterialRalMetadata
	httpClient *http.Client
	logger     logr.Logger
}

type prometheusMulticriterialRalMetadata struct {
	serverAddress       string
	metricName          string
	query1              string
	query2              string
	query3              string
	query4              string
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

type promMulticriterialRalQueryResult struct {
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

// NewPrometheusMulticriterialRalScaler creates a new prometheusMulticriterialRalScaler
func NewPrometheusMulticriterialRalScaler(config *ScalerConfig) (Scaler, error) {
	metricType, err := GetMetricTargetType(config)
	if err != nil {
		return nil, fmt.Errorf("error getting scaler metric type: %s", err)
	}

	logger := InitializeLogger(config, "prometheus_multicriterial_ral_scaler")

	meta, err := parsePrometheusMulticriterialRalMetadata(config)
	if err != nil {
		return nil, fmt.Errorf("error parsing prometheus multicriterial ral metadata: %s", err)
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

	return &prometheusMulticriterialRalScaler{
		metricType: metricType,
		metadata:   meta,
		httpClient: httpClient,
		logger:     logger,
	}, nil
}

func parsePrometheusMulticriterialRalMetadata(config *ScalerConfig) (meta *prometheusMulticriterialRalMetadata, err error) {
	meta = &prometheusMulticriterialRalMetadata{}

	if val, ok := config.TriggerMetadata[promMulticriterialRalServerAddress]; ok && val != "" {
		meta.serverAddress = val
	} else {
		return nil, fmt.Errorf("no %s given", promMulticriterialRalServerAddress)
	}

	if val, ok := config.TriggerMetadata[promMulticriterialRalMetricName]; ok && val != "" {
		meta.metricName = val
	} else {
		return nil, fmt.Errorf("no %s given", promMulticriterialRalMetricName)
	}

	if val, ok := config.TriggerMetadata[promMulticriterialQuery1]; ok && val != "" {
		meta.query1 = val
	} else {
		return nil, fmt.Errorf("no %s given", promMulticriterialQuery1)
	}

	if val, ok := config.TriggerMetadata[promMulticriterialQuery2]; ok && val != "" {
		meta.query2 = val
	} else {
		return nil, fmt.Errorf("no %s given", promMulticriterialQuery2)
	}

	if val, ok := config.TriggerMetadata[promMulticriterialQuery3]; ok && val != "" {
		meta.query3 = val
	} else {
		return nil, fmt.Errorf("no %s given", promMulticriterialQuery3)
	}

	if val, ok := config.TriggerMetadata[promMulticriterialQuery4]; ok && val != "" {
		meta.query4 = val
	} else {
		return nil, fmt.Errorf("no %s given", promMulticriterialQuery4)
	}

	meta.activationThreshold = 0
	if val, ok := config.TriggerMetadata[promMulticriterialRalActivationThreshold]; ok {
		t, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return nil, fmt.Errorf("activationThreshold parsing error %s", err.Error())
		}

		meta.activationThreshold = t
	}

	if val, ok := config.TriggerMetadata[promMulticriterialRalNamespace]; ok && val != "" {
		meta.namespace = val
	}

	if val, ok := config.TriggerMetadata[promMulticriterialRalCortexScopeOrgID]; ok && val != "" {
		meta.cortexOrgID = val
	}

	meta.ignoreNullValues = promMulticriterialRaldefaultIgnoreNullValues
	if val, ok := config.TriggerMetadata[promMulticriterialRalIgnoreNullValues]; ok && val != "" {
		ignoreNullValues, err := strconv.ParseBool(val)
		if err != nil {
			return nil, fmt.Errorf("err incorrect value for ignoreNullValues given: %s, "+
				"please use true or false", val)
		}
		meta.ignoreNullValues = ignoreNullValues
	}

	meta.unsafeSsl = false
	if val, ok := config.TriggerMetadata[promMulticriterialRalunsafeSsl]; ok && val != "" {
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

func (s *prometheusMulticriterialRalScaler) IsActive(ctx context.Context) (bool, error) {
	val, err := s.ExecutePromQuery1(ctx)
	if err != nil {
		s.logger.Error(err, "error executing prometheus multicriterial ral query")
		return false, err
	}

	return val > s.metadata.activationThreshold, nil
}

func (s *prometheusMulticriterialRalScaler) Close(context.Context) error {
	return nil
}

func (s *prometheusMulticriterialRalScaler) GetMetricSpecForScaling(context.Context) []v2.MetricSpec {
	metricName := kedautil.NormalizeString(fmt.Sprintf("prometheus-multicriterial-ral-%s", s.metadata.metricName))

	totalThreshold := 1.0 // desiredMetricValue

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

func (s *prometheusMulticriterialRalScaler) ExecutePromQuery1(ctx context.Context) (float64, error) {
	t := time.Now().UTC().Format(time.RFC3339)

	queryEscaped := url_pkg.QueryEscape(s.metadata.query1)
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
		req.Header.Add(promMulticriterialRalCortexHeaderKey, s.metadata.cortexOrgID)
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
		err := fmt.Errorf("prometheus multicriterial ral query api returned error. status: %d response: %s", r.StatusCode, string(b))
		s.logger.Error(err, "prometheus multicriterial ral query api returned error")
		return -1, err
	}

	var result promMulticriterialRalQueryResult
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
		return -1, fmt.Errorf("prometheus multicriterial ral metrics %s target may be lost, the result is empty", s.metadata.metricName)
	} else if len(result.Data.Result) > 1 {
		return -1, fmt.Errorf("prometheus multicriterial ral query %s returned multiple elements", s.metadata.query1)
	}

	valueLen := len(result.Data.Result[0].Value)
	if valueLen == 0 {
		if s.metadata.ignoreNullValues {
			return 0, nil
		}
		return -1, fmt.Errorf("prometheus multicriterial ral metrics %s target may be lost, the value list is empty", s.metadata.metricName)
	} else if valueLen < 2 {
		return -1, fmt.Errorf("prometheus multicriterial ral query %s didn't return enough values", s.metadata.query1)
	}

	val := result.Data.Result[0].Value[1]
	if val != nil {
		str := val.(string)
		v, err = strconv.ParseFloat(str, 64)
		if err != nil {
			s.logger.Error(err, "Error converting prometheus multicriterial ral value", "prometheus_multicriterial_ral_value", str)
			return -1, err
		}
	}

	return v, nil
}

func (s *prometheusMulticriterialRalScaler) ExecutePromQuery2(ctx context.Context) (float64, error) {
	t := time.Now().UTC().Format(time.RFC3339)

	queryEscaped := url_pkg.QueryEscape(s.metadata.query2)
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
		req.Header.Add(promMulticriterialRalCortexHeaderKey, s.metadata.cortexOrgID)
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
		err := fmt.Errorf("prometheus multicriterial ral query api returned error. status: %d response: %s", r.StatusCode, string(b))
		s.logger.Error(err, "prometheus multicriterial ral query api returned error")
		return -1, err
	}

	var result promMulticriterialRalQueryResult
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
		return -1, fmt.Errorf("prometheus multicriterial ral metrics %s target may be lost, the result is empty", s.metadata.metricName)
	} else if len(result.Data.Result) > 1 {
		return -1, fmt.Errorf("prometheus multicriterial ral query %s returned multiple elements", s.metadata.query2)
	}

	valueLen := len(result.Data.Result[0].Value)
	if valueLen == 0 {
		if s.metadata.ignoreNullValues {
			return 0, nil
		}
		return -1, fmt.Errorf("prometheus multicriterial ral metrics %s target may be lost, the value list is empty", s.metadata.metricName)
	} else if valueLen < 2 {
		return -1, fmt.Errorf("prometheus multicriterial ral query %s didn't return enough values", s.metadata.query2)
	}

	val := result.Data.Result[0].Value[1]
	if val != nil {
		str := val.(string)
		v, err = strconv.ParseFloat(str, 64)
		if err != nil {
			s.logger.Error(err, "Error converting prometheus multicriterial ral value", "prometheus_multicriterial_ral_value", str)
			return -1, err
		}
	}

	return v, nil
}

func (s *prometheusMulticriterialRalScaler) ExecutePromQuery3(ctx context.Context) (float64, error) {
	t := time.Now().UTC().Format(time.RFC3339)

	queryEscaped := url_pkg.QueryEscape(s.metadata.query3)
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
		req.Header.Add(promMulticriterialRalCortexHeaderKey, s.metadata.cortexOrgID)
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
		err := fmt.Errorf("prometheus multicriterial ral query api returned error. status: %d response: %s", r.StatusCode, string(b))
		s.logger.Error(err, "prometheus multicriterial ral query api returned error")
		return -1, err
	}

	var result promMulticriterialRalQueryResult
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
		return -1, fmt.Errorf("prometheus multicriterial ral metrics %s target may be lost, the result is empty", s.metadata.metricName)
	} else if len(result.Data.Result) > 1 {
		return -1, fmt.Errorf("prometheus multicriterial ral query %s returned multiple elements", s.metadata.query3)
	}

	valueLen := len(result.Data.Result[0].Value)
	if valueLen == 0 {
		if s.metadata.ignoreNullValues {
			return 0, nil
		}
		return -1, fmt.Errorf("prometheus multicriterial ral metrics %s target may be lost, the value list is empty", s.metadata.metricName)
	} else if valueLen < 2 {
		return -1, fmt.Errorf("prometheus multicriterial ral query %s didn't return enough values", s.metadata.query3)
	}

	val := result.Data.Result[0].Value[1]
	if val != nil {
		str := val.(string)
		v, err = strconv.ParseFloat(str, 64)
		if err != nil {
			s.logger.Error(err, "Error converting prometheus multicriterial ral value", "prometheus_multicriterial_ral_value", str)
			return -1, err
		}
	}

	return v, nil
}

func (s *prometheusMulticriterialRalScaler) ExecutePromQuery4(ctx context.Context) (float64, error) {
	t := time.Now().UTC().Format(time.RFC3339)

	queryEscaped := url_pkg.QueryEscape(s.metadata.query4)
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
		req.Header.Add(promMulticriterialRalCortexHeaderKey, s.metadata.cortexOrgID)
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
		err := fmt.Errorf("prometheus multicriterial ral query api returned error. status: %d response: %s", r.StatusCode, string(b))
		s.logger.Error(err, "prometheus multicriterial ral query api returned error")
		return -1, err
	}

	var result promMulticriterialRalQueryResult
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
		return -1, fmt.Errorf("prometheus multicriterial ral metrics %s target may be lost, the result is empty", s.metadata.metricName)
	} else if len(result.Data.Result) > 1 {
		return -1, fmt.Errorf("prometheus multicriterial ral query %s returned multiple elements", s.metadata.query4)
	}

	valueLen := len(result.Data.Result[0].Value)
	if valueLen == 0 {
		if s.metadata.ignoreNullValues {
			return 0, nil
		}
		return -1, fmt.Errorf("prometheus multicriterial ral metrics %s target may be lost, the value list is empty", s.metadata.metricName)
	} else if valueLen < 2 {
		return -1, fmt.Errorf("prometheus multicriterial ral query %s didn't return enough values", s.metadata.query4)
	}

	val := result.Data.Result[0].Value[1]
	if val != nil {
		str := val.(string)
		v, err = strconv.ParseFloat(str, 64)
		if err != nil {
			s.logger.Error(err, "Error converting prometheus multicriterial ral value", "prometheus_multicriterial_ral_value", str)
			return -1, err
		}
	}

	return v, nil
}

func (s *prometheusMulticriterialRalScaler) GetMetrics(ctx context.Context, metricName string, _ labels.Selector) ([]external_metrics.ExternalMetricValue, error) {
	val1, err1 := s.ExecutePromQuery1(ctx)

	if err1 != nil {
		s.logger.Error(err1, "error executing prometheus multicriterial ral query")
		return []external_metrics.ExternalMetricValue{}, err1
	}

	val2, err2 := s.ExecutePromQuery2(ctx)

	if err2 != nil {
		s.logger.Error(err2, "error executing prometheus multicriterial ral query")
		return []external_metrics.ExternalMetricValue{}, err2
	}

	val3, err3 := s.ExecutePromQuery3(ctx)

	if err3 != nil {
		s.logger.Error(err3, "error executing prometheus multicriterial ral query")
		return []external_metrics.ExternalMetricValue{}, err3
	}

	val4, err4 := s.ExecutePromQuery4(ctx)

	if err4 != nil {
		s.logger.Error(err4, "error executing prometheus multicriterial ral query")
		return []external_metrics.ExternalMetricValue{}, err4
	}

	// scaledObject := &kedav1alpha1.ScaledObject{}

	currentReplicas := (val1 + val2 + val3) / val4

	if currentReplicas < 1.0 {
		currentReplicas = defaultHPAMinReplicas
	} else if currentReplicas > 4.0 {
		currentReplicas = defaultHPAMaxReplicas
	}

	// lastReplicasCount = currentReplicas
	// fullQuery := strconv.Itoa(int(currentReplicas))

	metric := GenerateMetricInMili(metricName, currentReplicas)

	return append([]external_metrics.ExternalMetricValue{}, metric), nil
}

// func getHPAMinReplicas(scaledObject *kedav1alpha1.ScaledObject) *int32 {
// 	if scaledObject.Spec.MinReplicaCount != nil && *scaledObject.Spec.MinReplicaCount > 0 {
// 		return scaledObject.Spec.MinReplicaCount
// 	}
// 	tmp := defaultHPAMinReplicas
// 	return &tmp
// }

// func getHPAMaxReplicas(scaledObject *kedav1alpha1.ScaledObject) int32 {
// 	if scaledObject.Spec.MaxReplicaCount != nil {
// 		return *scaledObject.Spec.MaxReplicaCount
// 	}
// 	return defaultHPAMaxReplicas
// }
