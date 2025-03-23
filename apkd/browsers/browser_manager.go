package browsers

import (
	"bufio"
	"embed"
	"log"
	"math/rand"
	"strings"
)

//go:embed user_agents.txt
var fs embed.FS
var userAgents []string

func init() {
	file, err := fs.Open("user_agents.txt")
	if err != nil {
		log.Fatalf("failed to open file: %s", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			userAgents = append(userAgents, line)
		}
	}

	if err := scanner.Err(); err != nil {
		log.Fatalf("error reading file: %s", err)
	}

	if len(userAgents) == 0 {
		log.Fatal("no user agents found in the file")
	}
}

func GetRandomUserAgent() string {
	if len(userAgents) == 0 {
		return ""
	}
	return userAgents[rand.Intn(len(userAgents))]
}
