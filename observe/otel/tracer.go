package oteltracer

import (
	"context"
	"fmt"
	"time"

	"gochen/contextx"
	gpropagation "gochen/contextx/propagation"
	"gochen/observe"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// Tracer 定义相关追踪器实现。
type Tracer struct {
	tracer     trace.Tracer
	propagator propagation.TextMapPropagator
}

// Option 定义相关可选配置函数。
type Option func(*Tracer)

// WithTracer 处理带追踪器。
func WithTracer(t trace.Tracer) Option {
	return func(tr *Tracer) {
		if t != nil {
			tr.tracer = t
		}
	}
}

// WithPropagator 处理带Propagator。
func WithPropagator(p propagation.TextMapPropagator) Option {
	return func(tr *Tracer) {
		if p != nil {
			tr.propagator = p
		}
	}
}

// New 创建追踪器。
func New(serviceName string, opts ...Option) *Tracer {
	tr := &Tracer{
		tracer:     otel.Tracer(serviceName),
		propagator: otel.GetTextMapPropagator(),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(tr)
		}
	}
	return tr
}

// StartSpan 创建新的 Span，并返回带链路信息的上下文。
func (t *Tracer) StartSpan(ctx context.Context, name string, opts ...observe.SpanOption) (context.Context, observe.ISpan) {
	cfg := &observe.SpanConfig{}
	for _, opt := range opts {
		if opt != nil {
			opt(cfg)
		}
	}

	startOpts := make([]trace.SpanStartOption, 0, 2)
	if cfg.Kind != observe.SpanKindUnspecified {
		startOpts = append(startOpts, trace.WithSpanKind(toOTELSpanKind(cfg.Kind)))
	}
	if len(cfg.Attributes) > 0 {
		startOpts = append(startOpts, trace.WithAttributes(toOTELAttributes(cfg.Attributes)...))
	}
	if len(cfg.Links) > 0 {
		links := make([]trace.Link, 0, len(cfg.Links))
		for _, l := range cfg.Links {
			if sc, ok := toOTELSpanContext(l.SpanContext); ok {
				links = append(links, trace.Link{SpanContext: sc, Attributes: toOTELAttributes(l.Attributes)})
			}
		}
		if len(links) > 0 {
			startOpts = append(startOpts, trace.WithLinks(links...))
		}
	}

	if ctx == nil {
		ctx = contextx.Background()
	}

	newCtx, sp := t.tracer.Start(ctx, name, startOpts...)
	return newCtx, &otelSpan{span: sp}
}

// Extract 提取信息。
func (t *Tracer) Extract(ctx context.Context, carrier gpropagation.IEnumerableCarrier) context.Context {
	if ctx == nil {
		ctx = contextx.Background()
	}
	if carrier == nil {
		return ctx
	}
	return t.propagator.Extract(ctx, carrierAdapter{carrier: carrier})
}

// Inject 把当前链路上下文写入传输载体。
func (t *Tracer) Inject(ctx context.Context, carrier gpropagation.IEnumerableCarrier) {
	if ctx == nil || carrier == nil {
		return
	}
	t.propagator.Inject(ctx, carrierAdapter{carrier: carrier})
}

type otelSpan struct {
	span trace.Span
}

// End 结束当前 Span。
func (s *otelSpan) End() { s.span.End() }

// SetAttribute 为当前 Span 记录单个属性。
func (s *otelSpan) SetAttribute(key string, value any) {
	s.span.SetAttributes(toOTELAttribute(key, value))
}

// SetAttributes 为当前 Span 批量记录属性。
func (s *otelSpan) SetAttributes(attrs map[string]any) {
	s.span.SetAttributes(toOTELAttributes(attrs)...)
}

// AddEvent 添加事件。
func (s *otelSpan) AddEvent(name string, attrs ...map[string]any) {
	if len(attrs) == 0 || attrs[0] == nil {
		s.span.AddEvent(name)
		return
	}
	s.span.AddEvent(name, trace.WithAttributes(toOTELAttributes(attrs[0])...))
}

// RecordError 为当前 Span 记录错误。
func (s *otelSpan) RecordError(err error) {
	if err == nil {
		return
	}
	s.span.RecordError(err)
	s.span.SetStatus(codes.Error, err.Error())
}

// SetStatus 设置当前 Span 的状态。
func (s *otelSpan) SetStatus(code string, description string) {
	switch code {
	case "ok":
		s.span.SetStatus(codes.Ok, description)
	case "error":
		s.span.SetStatus(codes.Error, description)
	default:
		s.span.SetStatus(codes.Unset, description)
	}
}

// SpanContext 返回当前 Span 的链路上下文。
func (s *otelSpan) SpanContext() observe.SpanContext {
	sc := s.span.SpanContext()
	return observe.SpanContext{
		TraceID:    sc.TraceID().String(),
		SpanID:     sc.SpanID().String(),
		TraceFlags: byte(sc.TraceFlags()),
	}
}

type carrierAdapter struct {
	carrier gpropagation.IEnumerableCarrier
}

func (a carrierAdapter) Get(key string) string {
	value, _ := a.carrier.Get(key)
	return value
}

func (a carrierAdapter) Set(key, value string) { a.carrier.Set(key, value) }

func (a carrierAdapter) Keys() []string { return a.carrier.Keys() }

// toOTELSpanKind 转换OTELSpanKind。
func toOTELSpanKind(kind observe.SpanKind) trace.SpanKind {
	switch kind {
	case observe.SpanKindInternal:
		return trace.SpanKindInternal
	case observe.SpanKindServer:
		return trace.SpanKindServer
	case observe.SpanKindClient:
		return trace.SpanKindClient
	case observe.SpanKindProducer:
		return trace.SpanKindProducer
	case observe.SpanKindConsumer:
		return trace.SpanKindConsumer
	default:
		return trace.SpanKindUnspecified
	}
}

// toOTELAttributes 转换OTELAttributes。
func toOTELAttributes(attrs map[string]any) []attribute.KeyValue {
	if len(attrs) == 0 {
		return nil
	}
	out := make([]attribute.KeyValue, 0, len(attrs))
	for k, v := range attrs {
		out = append(out, toOTELAttribute(k, v))
	}
	return out
}

// toOTELAttribute 转换OTELAttribute。
func toOTELAttribute(key string, value any) attribute.KeyValue {
	k := attribute.Key(key)
	switch v := value.(type) {
	case string:
		return k.String(v)
	case []string:
		return k.StringSlice(v)
	case bool:
		return k.Bool(v)
	case int:
		return k.Int(v)
	case int64:
		return k.Int64(v)
	case uint64:
		return k.Int64(int64(v))
	case float64:
		return k.Float64(v)
	case float32:
		return k.Float64(float64(v))
	case time.Duration:
		return k.Int64(v.Milliseconds())
	default:
		return k.String(fmt.Sprint(value))
	}
}

// toOTELSpanContext 转换OTELSpan上下文。
func toOTELSpanContext(sc observe.SpanContext) (trace.SpanContext, bool) {
	if !sc.IsValid() {
		return trace.SpanContext{}, false
	}
	tid, err := trace.TraceIDFromHex(sc.TraceID)
	if err != nil {
		return trace.SpanContext{}, false
	}
	sid, err := trace.SpanIDFromHex(sc.SpanID)
	if err != nil {
		return trace.SpanContext{}, false
	}
	return trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    tid,
		SpanID:     sid,
		TraceFlags: trace.TraceFlags(sc.TraceFlags),
	}), true
}

var _ observe.ITracer = (*Tracer)(nil)
var _ observe.ISpan = (*otelSpan)(nil)
