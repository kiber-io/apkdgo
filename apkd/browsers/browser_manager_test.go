package browsers

import (
	"strings"
	"testing"
)

func TestGetRandomUserAgent(t *testing.T) {
	if userAgents == "" {
		t.Errorf("user_agents.txt should not be empty")
	}

	agents := strings.Split(userAgents, "\n")
	for i := 0; i < 10; i++ {
		ua := GetRandomUserAgent()

		if !contains(agents, ua) {
			t.Errorf("GetRandomUserAgent returned an invalid user agent: %s", ua)
		}
	}
}

func contains(s []string, e string) bool {
	for _, a := range s {
		if a == e {
			return true
		}
	}
	return false
}
