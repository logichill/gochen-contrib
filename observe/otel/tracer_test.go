package oteltracer

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"gochen/contextx/propagation"
	"gochen/observe"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	otelpropagation "go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/embedded"
)

func TestTracer_NoPanic(t *testing.T) {
	tr := New("svc")
	ctx, span := tr.StartSpan(context.Background(), "op", observe.WithAttribute("k", "v"))
	span.AddEvent("e1")
	span.SetStatus("ok", "")
	span.End()

	carrier := propagation.MapCarrier{}
	tr.Inject(ctx, carrier)
	_ = tr.Extract(context.Background(), carrier)
}

func TestTracer_RecordsAttributesErrorsAndStatus(t *testing.T) {
	recorder := &recordingTracer{}
	tr := New("svc", WithTracer(recorder))

	ctx, span := tr.StartSpan(context.Background(), "operation",
		observe.WithSpanKind(observe.SpanKindServer),
		observe.WithAttribute("request.id", "req-1"),
		observe.WithAttribute("attempt", int64(2)),
		observe.WithAttribute("timeout", 1500*time.Millisecond),
	)
	if ctx == nil {
		t.Fatal("StartSpan returned nil context")
	}
	span.SetAttribute("success", true)
	span.SetAttributes(map[string]any{"ratio": 0.5})
	span.AddEvent("cleanup", map[string]any{"resource": "subscription"})
	span.RecordError(errors.New("cleanup failed"))
	span.End()

	if recorder.span == nil {
		t.Fatal("tracer did not create a span")
	}
	if recorder.span.kind != trace.SpanKindServer {
		t.Fatalf("span kind = %v, want server", recorder.span.kind)
	}
	if got := recorder.span.attributes["request.id"].AsString(); got != "req-1" {
		t.Fatalf("request.id = %q, want req-1", got)
	}
	if got := recorder.span.attributes["attempt"].AsInt64(); got != 2 {
		t.Fatalf("attempt = %d, want 2", got)
	}
	if got := recorder.span.attributes["timeout"].AsInt64(); got != 1500 {
		t.Fatalf("timeout milliseconds = %d, want 1500", got)
	}
	if got := recorder.span.attributes["success"].AsBool(); !got {
		t.Fatal("success attribute was not recorded")
	}
	if len(recorder.span.events) != 2 { // cleanup + exception from RecordError.
		t.Fatalf("events = %d, want 2", len(recorder.span.events))
	}
	if recorder.span.status != codes.Error || recorder.span.statusDescription != "cleanup failed" {
		t.Fatalf("status = (%v, %q), want error/cleanup failed", recorder.span.status, recorder.span.statusDescription)
	}
	if !recorder.span.ended {
		t.Fatal("span was not ended")
	}
}

func TestTracer_InjectExtractAndLinks(t *testing.T) {
	recorder := &recordingTracer{}
	tr := New("svc", WithTracer(recorder), WithPropagator(otelpropagation.TraceContext{}))
	ctx, span := tr.StartSpan(context.Background(), "parent")
	parent := span.SpanContext()
	if !parent.IsValid() {
		t.Fatal("expected valid span context")
	}

	carrier := propagation.NewMapCarrier()
	tr.Inject(ctx, carrier)
	extracted := tr.Extract(context.Background(), carrier)
	if got := trace.SpanContextFromContext(extracted); !got.IsValid() || got.TraceID().String() != parent.TraceID {
		t.Fatalf("extracted span context = %v, want trace %s", got, parent.TraceID)
	}

	_, child := tr.StartSpan(context.Background(), "child", func(cfg *observe.SpanConfig) {
		cfg.Links = append(cfg.Links, observe.Link{SpanContext: parent})
	})
	child.End()
	if len(recorder.spans) != 2 || len(recorder.spans[1].links) != 1 {
		t.Fatalf("recorded spans/links = %d/%d, want 2/1", len(recorder.spans), len(recorder.spans[1].links))
	}
}

type recordingTracer struct {
	embedded.Tracer
	mu    sync.Mutex
	span  *recordingSpan
	spans []*recordingSpan
}

func (t *recordingTracer) Start(ctx context.Context, _ string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	cfg := trace.NewSpanStartConfig(opts...)
	span := &recordingSpan{
		kind:       cfg.SpanKind(),
		attributes: make(map[string]attribute.Value),
		spanCtx: trace.NewSpanContext(trace.SpanContextConfig{
			TraceID:    mustTraceID("00112233445566778899aabbccddeeff"),
			SpanID:     mustSpanID("0011223344556677"),
			TraceFlags: trace.FlagsSampled,
		}),
	}
	for _, attr := range cfg.Attributes() {
		span.attributes[string(attr.Key)] = attr.Value
	}
	span.links = append(span.links, cfg.Links()...)
	t.mu.Lock()
	t.span = span
	t.spans = append(t.spans, span)
	t.mu.Unlock()
	return trace.ContextWithSpan(ctx, span), span
}

type recordingSpan struct {
	embedded.Span
	mu                sync.Mutex
	spanCtx           trace.SpanContext
	kind              trace.SpanKind
	attributes        map[string]attribute.Value
	links             []trace.Link
	events            []string
	status            codes.Code
	statusDescription string
	ended             bool
}

func (s *recordingSpan) End(...trace.SpanEndOption) {
	s.mu.Lock()
	s.ended = true
	s.mu.Unlock()
}

func (s *recordingSpan) AddEvent(name string, _ ...trace.EventOption) {
	s.mu.Lock()
	s.events = append(s.events, name)
	s.mu.Unlock()
}

func (s *recordingSpan) AddLink(link trace.Link) { s.links = append(s.links, link) }

func (s *recordingSpan) IsRecording() bool { return true }

func (s *recordingSpan) RecordError(_ error, _ ...trace.EventOption) {
	s.mu.Lock()
	s.events = append(s.events, "exception")
	s.mu.Unlock()
}

func (s *recordingSpan) SpanContext() trace.SpanContext { return s.spanCtx }

func (s *recordingSpan) SetStatus(code codes.Code, description string) {
	s.mu.Lock()
	s.status = code
	s.statusDescription = description
	s.mu.Unlock()
}

func (s *recordingSpan) SetName(string) {}

func (s *recordingSpan) SetAttributes(attrs ...attribute.KeyValue) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, attr := range attrs {
		s.attributes[string(attr.Key)] = attr.Value
	}
}

func (s *recordingSpan) TracerProvider() trace.TracerProvider { return nil }

func mustTraceID(value string) trace.TraceID {
	id, err := trace.TraceIDFromHex(value)
	if err != nil {
		panic(err)
	}
	return id
}

func mustSpanID(value string) trace.SpanID {
	id, err := trace.SpanIDFromHex(value)
	if err != nil {
		panic(err)
	}
	return id
}
