package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	if err := check(ctx, os.Getenv("UPSTASH_RELEASE_URL")); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("Upstash TLS GETDEL, Lua and Pub/Sub verified")
}

func check(ctx context.Context, rawURL string) error {
	options, err := redis.ParseURL(rawURL)
	if err != nil || options.TLSConfig == nil {
		return errors.New("a rediss:// Upstash connection is required")
	}
	host, _, err := net.SplitHostPort(options.Addr)
	if err != nil || !strings.HasSuffix(host, ".upstash.io") {
		return errors.New("the release cache must use an Upstash hostname")
	}
	options.TLSConfig.MinVersion = tls.VersionTLS12
	options.MaxRetries = -1
	options.DialTimeout = 3 * time.Second
	options.ReadTimeout = 3 * time.Second
	options.WriteTimeout = 3 * time.Second
	cache := redis.NewClient(options)
	defer cache.Close()

	const probeValue = "ready"
	key := "release:check:" + uuid.NewString()
	if err := cache.Set(ctx, key, probeValue, time.Minute).Err(); err != nil {
		return errors.New("TLS connection or expiring write failed")
	}
	if value, err := cache.GetDel(ctx, key).Result(); err != nil || value != probeValue {
		return errors.New("GETDEL check failed")
	}
	if result, err := cache.Eval(ctx, "return ARGV[1]", nil, probeValue).Text(); err != nil || result != probeValue {
		return errors.New("the Lua capability check failed")
	}
	subscription := cache.Subscribe(ctx, key)
	defer subscription.Close()
	if _, err := subscription.Receive(ctx); err != nil {
		return errors.New("Pub/Sub subscribe failed")
	}
	if err := cache.Publish(ctx, key, probeValue).Err(); err != nil {
		return errors.New("Pub/Sub publish failed")
	}
	message, err := subscription.ReceiveMessage(ctx)
	if err != nil || message.Payload != probeValue {
		return errors.New("Pub/Sub receive failed")
	}
	return nil
}
