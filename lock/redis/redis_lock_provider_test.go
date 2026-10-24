package redislock

import (
	"context"
	"sync"
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
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout lost DeadlineExceeded cause: %v", err)
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

func TestProvider_AcquireLeaseMaxDurationDisabled(t *testing.T) {
	_, client := setupTestRedis(t)
	ttl := 30 * time.Millisecond
	p, err := New(client, &Config{
		Owner:            "a",
		KeyPrefix:        "t:",
		TTL:              ttl,
		MaxLeaseDuration: -1,
		PollInterval:     5 * time.Millisecond,
		Logger:           logging.NewNoopLogger(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if p.maxLeaseDuration >= 0 {
		t.Fatalf("expected negative maxLeaseDuration to disable cap, got %v", p.maxLeaseDuration)
	}

	lease, err := p.AcquireLease(context.Background(), "k")
	if err != nil {
		t.Fatalf("AcquireLease: %v", err)
	}
	defer lease.Release()

	// 持锁超过默认上限 5*TTL（150ms），禁用上限后看门狗应持续续租且不触发 Lost。
	select {
	case err := <-lease.Lost():
		t.Fatalf("expected lease to remain active when MaxLeaseDuration < 0, got Lost: %v", err)
	case <-time.After(6 * ttl):
	}
}

// TestNew_DoesNotMutateConfig 验证成功与失败构造路径都不改写调用方配置：
// 默认值归一化只发生在内部副本上。
func TestNew_DoesNotMutateConfig(t *testing.T) {
	_, client := setupTestRedis(t)

	if _, err := New(nil, &Config{Owner: "a"}); err == nil {
		t.Fatal("expected error for nil client")
	}

	emptyOwner := &Config{KeyPrefix: "keep:"}
	if _, err := New(client, emptyOwner); err == nil {
		t.Fatal("expected error for empty owner")
	}
	if emptyOwner.KeyPrefix != "keep:" || emptyOwner.TTL != 0 || emptyOwner.PollInterval != 0 ||
		emptyOwner.MaxLeaseDuration != 0 || emptyOwner.Logger != nil {
		t.Fatalf("failed construction mutated caller config: %+v", emptyOwner)
	}

	// 成功路径：内部完成归一化，输入对象仍为原始零值。
	zero := &Config{Owner: "a"}
	p, err := New(client, zero)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if zero.KeyPrefix != "" || zero.TTL != 0 || zero.PollInterval != 0 ||
		zero.MaxLeaseDuration != 0 || zero.Logger != nil {
		t.Fatalf("constructor mutated caller config: %+v", zero)
	}
	if p.keyPrefix != "distributed_locks:" || p.ttl != 30*time.Second ||
		p.pollInterval != 50*time.Millisecond || p.maxLeaseDuration != 150*time.Second || p.logger == nil {
		t.Fatalf("normalized values not applied: prefix=%q ttl=%v poll=%v max=%v",
			p.keyPrefix, p.ttl, p.pollInterval, p.maxLeaseDuration)
	}
}

// TestNew_ConcurrentSharedConfig 用同一配置指针并发构造，
// 在 race 检测下验证构造器只读输入配置。
func TestNew_ConcurrentSharedConfig(t *testing.T) {
	_, client := setupTestRedis(t)
	shared := &Config{Owner: "a"}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, err := New(client, shared)
			if err != nil {
				t.Errorf("New: %v", err)
				return
			}
			if p.ttl != 30*time.Second || p.keyPrefix != "distributed_locks:" {
				t.Errorf("normalized values not applied: ttl=%v prefix=%q", p.ttl, p.keyPrefix)
			}
		}()
	}
	wg.Wait()

	if shared.KeyPrefix != "" || shared.TTL != 0 || shared.PollInterval != 0 ||
		shared.MaxLeaseDuration != 0 || shared.Logger != nil {
		t.Fatalf("concurrent construction mutated shared config: %+v", shared)
	}
}

// TestProvider_AcquireLeaseLostWhenRedisUnreachable 验证 Redis 假死（网络分区）时
// 看门狗能在 TTL 内发出 Lost 信号。
//
// 时序：TTL=90ms → 续租间隔 30ms；假死发生在首次续租前，3 次尝试（各 10ms 超时
// + 退避）全部失败后 elapsed 远小于 TTL，看门狗进入快速重试；分区持续超过 TTL
// 后，下一次续租循环以 Cache 错误码关 Lost——持有方在锁实际过期前后即收到通知，
// 而不是等业务侧撞上互斥失效。
func TestProvider_AcquireLeaseLostWhenRedisUnreachable(t *testing.T) {
	s, client := setupTestRedis(t)
	p, err := New(client, &Config{
		Owner:        "a",
		KeyPrefix:    "t:",
		TTL:          90 * time.Millisecond,
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

	// Redis 假死：所有命令返回错误，模拟网络分区。
	s.SetError("SIMULATED NETWORK PARTITION")

	// 分区持续超过整个 TTL，Lost 必须触发且不得晚于太多（留出调度余量）。
	deadline := time.Now().Add(2 * p.ttl)
	select {
	case err := <-lease.Lost():
		if err == nil {
			t.Fatal("expected error on lost lease")
		}
		if errors.Code(err) != errors.Cache {
			t.Fatalf("expected CACHE error code, got %v", err)
		}
	case <-time.After(time.Until(deadline)):
		t.Fatal("expected Lost signal within lease TTL after partition")
	}
}
