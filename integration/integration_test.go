package integration

import "testing"

func TestIntegrationRequiresFixture(t *testing.T) {
	t.Skip("integration tests require a reachable 9front host and are not configured in this environment")
}
