package redislock

import (
	"context"
	"testing"
	"time"

	"gochen/errors"
	"gochen/observe/logging"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func setupTestRedis(t *testing.T) (*miniredis.Miniredis, redis.UniversalClient) {
	t.Helper()

	s, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	client := redis.NewClient(&redis.Options{Addr: s.Addr()})
	t.Cleanup(func() {
		_ = client.Close()
		s.Close()
	})
	return s, client
}

func TestProvider_AcquireAndRelease(t *testing.T) {
	_, client := setupTestRedis(t)
	p, err := New(client, &Config{
		Owner:        "a",
		KeyPrefix:    "t:",
		TTL:          200 * time.Millisecond,
		PollInterval: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	release, err := p.Acquire(context.Background(), "k")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	release()
	// 幂等：二次 release 不应 panic
	release()
}

func TestProvider_AcquireTimeout(t *testing.T) {
	_, client := setupTestRedis(t)

	p1, err := New(client, &Config{
		Owner:        "a",
		KeyPrefix:    "t:",
		TTL:          5 * time.Second,
		PollInterval: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	p2, err := New(client, &Config{
		Owner:        "b",
		KeyPrefix:    "t:",
		TTL:          5 * time.Second,
		PollInterval: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	release, err := p1.Acquire(context.Background(), "k")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer release()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err = p2.Acquire(ctx, "k")
	if err == nil {
		t.Fatalf("expected timeout error, got nil")
	}
	if errors.Code(err) != errors.Timeout {
		t.Fatalf("expected TIMEOUT, got %v", err)
	}
}

func TestProvider_ExpiresAndCanBeReacquired(t *testing.T) {
	s, client := setupTestRedis(t)

	p1, err := New(client, &Config{
		Owner:        "a",
		KeyPrefix:    "t:",
		TTL:          30 * time.Millisecond,
		PollInterval: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	p2, err := New(client, &Config{
		Owner:        "b",
		KeyPrefix:    "t:",
		TTL:          30 * time.Millisecond,
		PollInterval: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	release, err := p1.Acquire(context.Background(), "k")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	// 故意不 release，等待 TTL 过期
	_ = release
	s.FastForward(80 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	r2, err := p2.Acquire(ctx, "k")
	if err != nil {
		t.Fatalf("Acquire after expire: %v", err)
	}
	r2()
}

func TestProvider_ReleaseIsSafeWithToken(t *testing.T) {
	s, client := setupTestRedis(t)

	p1, err := New(client, &Config{
		Owner:        "a",
		KeyPrefix:    "t:",
		TTL:          30 * time.Millisecond,
		PollInterval: 1 * time.Millisecond,
		Logger:       logging.NewNoopLogger(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	p2, err := New(client, &Config{
		Owner:        "b",
		KeyPrefix:    "t:",
		TTL:          200 * time.Millisecond,
		PollInterval: 1 * time.Millisecond,
		Logger:       logging.NewNoopLogger(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	release1, err := p1.Acquire(context.Background(), "k")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	// 等待 TTL 过期后由 p2 获取新锁
	s.FastForward(80 * time.Millisecond)
	release2, err := p2.Acquire(context.Background(), "k")
	if err != nil {
		t.Fatalf("Acquire2: %v", err)
	}
	defer release2()

	// p1 的 release 不能删除 p2 的锁
	release1()

	val, err := client.Get(context.Background(), "t:k").Result()
	if err != nil || val == "" {
		t.Fatalf("expected lock still present, got val=%q err=%v", val, err)
	}
}

func TestProvider_AcquireLeaseNormalRelease(t *testing.T) {
	_, client := setupTestRedis(t)
	p, err := New(client, &Config{
		Owner:        "a",
		KeyPrefix:    "t:",
		TTL:          200 * time.Millisecond,
		PollInterval: 5 * time.Millisecond,
		Logger:       logging.NewNoopLogger(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	lease, err := p.AcquireLease(context.Background(), "k")
	if err != nil {
		t.Fatalf("AcquireLease: %v", err)
	}

	lease.Release()

	select {
	case err, ok := <-lease.Lost():
		if ok && err != nil {
			t.Fatalf("expected no error on normal release, got %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected Lost channel to close upon Release")
	}
}

func TestProvider_AcquireLeaseLostSignalWhenOverwritten(t *testing.T) {
	_, client := setupTestRedis(t)
	p, err := New(client, &Config{
		Owner:        "a",
		KeyPrefix:    "t:",
		TTL:          60 * time.Millisecond,
		PollInterval: 5 * time.Millisecond,
		Logger:       logging.NewNoopLogger(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	lease, err := p.AcquireLease(context.Background(), "k")
	if err != nil {
		t.Fatalf("AcquireLease: %v", err)
	}
	defer lease.Release()

	// Simulate lock stolen by another process
	client.Set(context.Background(), "t:k", "stolen-by-another-owner", 10*time.Second)

	select {
	case err := <-lease.Lost():
		if err == nil {
			t.Fatal("expected error on lost lease")
		}
		if errors.Code(err) != errors.Conflict && errors.Code(err) != errors.Cache {
			t.Fatalf("expected CONFLICT or CACHE error code, got %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected Lost channel to trigger when lock is stolen")
	}
}

func TestProvider_AcquireLeaseMaxDurationExceeded(t *testing.T) {
	_, client := setupTestRedis(t)
	p, err := New(client, &Config{
		Owner:            "a",
		KeyPrefix:        "t:",
		TTL:              30 * time.Millisecond,
		MaxLeaseDuration: 50 * time.Millisecond,
		PollInterval:     5 * time.Millisecond,
		Logger:           logging.NewNoopLogger(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	lease, err := p.AcquireLease(context.Background(), "k")
	if err != nil {
		t.Fatalf("AcquireLease: %v", err)
	}
	defer lease.Release()

	select {
	case err := <-lease.Lost():
		if err == nil {
			t.Fatal("expected error when max lease duration is exceeded")
		}
		if errors.Code(err) != errors.Timeout {
			t.Fatalf("expected TIMEOUT error code, got %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected Lost channel to fire when max lease duration is reached")
	}
}
