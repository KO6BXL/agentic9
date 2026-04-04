package integration

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"agentic9/internal/config"
	"agentic9/internal/transport/tlsrcpu"
)

func TestVerifyAndExecAgainstRealHost(t *testing.T) {
	profile, secret, ok := loadIntegrationFixture()
	if !ok {
		t.Skip("set AGENTIC9_IT_CPU_HOST, AGENTIC9_IT_AUTH_HOST, AGENTIC9_IT_USER, AGENTIC9_IT_AUTH_DOMAIN, and AGENTIC9_IT_SECRET to run against a real 9front host")
	}

	client := tlsrcpu.NewClient(profile, secret)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := client.Verify(ctx); err != nil {
		t.Fatalf("Verify: %v", err)
	}

	const marker = "agentic9-integration-ok"
	var out strings.Builder
	if err := client.Exec(ctx, "echo "+marker+"\n", func(chunk []byte) error {
		_, _ = out.Write(chunk)
		return nil
	}); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if got := out.String(); !strings.Contains(got, marker) {
		t.Fatalf("output %q does not contain %q", got, marker)
	}
}

func loadIntegrationFixture() (config.Profile, config.Secret, bool) {
	cpuHost := os.Getenv("AGENTIC9_IT_CPU_HOST")
	authHost := os.Getenv("AGENTIC9_IT_AUTH_HOST")
	user := os.Getenv("AGENTIC9_IT_USER")
	authDomain := os.Getenv("AGENTIC9_IT_AUTH_DOMAIN")
	secret := os.Getenv("AGENTIC9_IT_SECRET")
	if cpuHost == "" || authHost == "" || user == "" || authDomain == "" || secret == "" {
		return config.Profile{}, config.Secret{}, false
	}
	return config.Profile{
			CPUHost:    cpuHost,
			AuthHost:   authHost,
			User:       user,
			AuthDomain: authDomain,
		}, config.Secret{
			Value:  secret,
			Source: fmt.Sprintf("env:%s", "AGENTIC9_IT_SECRET"),
		}, true
}
