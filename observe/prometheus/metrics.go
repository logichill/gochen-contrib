package prometheusmetrics

import (
	"net/http"
	"sort"
	"sync"
	"time"

	"gochen/observe"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics 定义相关指标收集器。
type Metrics struct {
	registry prometheus.Registerer
	gatherer prometheus.Gatherer

	mu         sync.RWMutex
	counters   map[string]counterMetric
	gauges     map[string]gaugeMetric
	histograms map[string]histogramMetric
}

type counterMetric struct {
	vec        *prometheus.CounterVec
	labelNames []string
}

type gaugeMetric struct {
	vec        *prometheus.GaugeVec
	labelNames []string
}

type histogramMetric struct {
	vec        *prometheus.HistogramVec
	labelNames []string
}

// New 创建指标。
func New() *Metrics {
	return NewWith(prometheus.DefaultRegisterer, prometheus.DefaultGatherer)
}

// NewWith 创建指标。
func NewWith(registry prometheus.Registerer, gatherer prometheus.Gatherer) *Metrics {
	if registry == nil {
		registry = prometheus.DefaultRegisterer
	}
	if gatherer == nil {
		gatherer = prometheus.DefaultGatherer
	}
	return &Metrics{
		registry:   registry,
		gatherer:   gatherer,
		counters:   make(map[string]counterMetric),
		gauges:     make(map[string]gaugeMetric),
		histograms: make(map[string]histogramMetric),
	}
}

// Handler 处理处理器。
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.gatherer, promhttp.HandlerOpts{})
}

// RegisterCounter 注册 counter。
func (m *Metrics) RegisterCounter(name, help string, labelNames []string) error {
	labelNames = normalizeLabelNames(labelNames)
	vec := prometheus.NewCounterVec(prometheus.CounterOpts{Name: name, Help: help}, labelNames)
	if err := m.registry.Register(vec); err != nil {
		return err
	}

	m.mu.Lock()
	m.counters[name] = counterMetric{vec: vec, labelNames: labelNames}
	m.mu.Unlock()
	return nil
}

// RegisterGauge 注册 gauge。
func (m *Metrics) RegisterGauge(name, help string, labelNames []string) error {
	labelNames = normalizeLabelNames(labelNames)
	vec := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: name, Help: help}, labelNames)
	if err := m.registry.Register(vec); err != nil {
		return err
	}

	m.mu.Lock()
	m.gauges[name] = gaugeMetric{vec: vec, labelNames: labelNames}
	m.mu.Unlock()
	return nil
}

// RegisterHistogram 注册 histogram。
func (m *Metrics) RegisterHistogram(name, help string, labelNames []string, buckets []float64) error {
	labelNames = normalizeLabelNames(labelNames)
	opts := prometheus.HistogramOpts{Name: name, Help: help}
	if len(buckets) > 0 {
		opts.Buckets = buckets
	}
	vec := prometheus.NewHistogramVec(opts, labelNames)
	if err := m.registry.Register(vec); err != nil {
		return err
	}

	m.mu.Lock()
	m.histograms[name] = histogramMetric{vec: vec, labelNames: labelNames}
	m.mu.Unlock()
	return nil
}

// Counter 处理Counter。
func (m *Metrics) Counter(name string, value int64, labels map[string]string) {
	m.mu.RLock()
	metric, ok := m.counters[name]
	m.mu.RUnlock()
	if !ok || metric.vec == nil {
		return
	}
	metric.vec.With(buildPromLabels(metric.labelNames, labels)).Add(float64(value))
}

// Gauge 处理Gauge。
func (m *Metrics) Gauge(name string, value float64, labels map[string]string) {
	m.mu.RLock()
	metric, ok := m.gauges[name]
	m.mu.RUnlock()
	if !ok || metric.vec == nil {
		return
	}
	metric.vec.With(buildPromLabels(metric.labelNames, labels)).Set(value)
}

// Histogram 处理Histogram。
func (m *Metrics) Histogram(name string, value float64, labels map[string]string) {
	m.mu.RLock()
	metric, ok := m.histograms[name]
	m.mu.RUnlock()
	if !ok || metric.vec == nil {
		return
	}
	metric.vec.With(buildPromLabels(metric.labelNames, labels)).Observe(value)
}

// Timer 处理定时器。
func (m *Metrics) Timer(name string, labels map[string]string) observe.ITimer {
	return &timer{metrics: m, name: name, labels: labels, start: time.Now()}
}

type timer struct {
	metrics *Metrics
	name    string
	labels  map[string]string
	start   time.Time
}

// Stop 停止定时器。
func (t *timer) Stop() {
	t.metrics.Histogram(t.name, float64(time.Since(t.start).Milliseconds()), t.labels)
}

// normalizeLabelNames 规范化标签Names。
func normalizeLabelNames(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	copied := append([]string{}, names...)
	sort.Strings(copied)

	out := copied[:0]
	var prev string
	for i, n := range copied {
		if i == 0 || n != prev {
			out = append(out, n)
			prev = n
		}
	}
	return out
}

// buildPromLabels 构造Prom标签集合。
func buildPromLabels(labelNames []string, labels map[string]string) prometheus.Labels {
	if len(labelNames) == 0 {
		return nil
	}
	out := make(prometheus.Labels, len(labelNames))
	for _, k := range labelNames {
		out[k] = ""
		if labels != nil {
			if v, ok := labels[k]; ok {
				out[k] = v
			}
		}
	}
	return out
}

var _ observe.IMetrics = (*Metrics)(nil)
