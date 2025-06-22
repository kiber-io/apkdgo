package browsers

import (
	_ "embed"
	"math/rand"
	"strings"
	"time"
)

//go:embed user_agents.txt
var userAgents string

func GetRandomUserAgent() string {
	randomSource := rand.NewSource(time.Now().UnixNano())
	random := rand.New(randomSource)

	agents := strings.Split(userAgents, "\n")

	return agents[random.Intn(len(agents))]
}
