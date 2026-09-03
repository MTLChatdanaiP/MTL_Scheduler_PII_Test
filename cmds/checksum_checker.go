// cmd/checksum/main.go
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"MTL_Scheduler_PII_Test/internals/models"
)

func main() {
	path := flag.String("policy", "policies/default.json", "path to the policy JSON file")
	flag.Parse()

	data, err := os.ReadFile(*path)
	if err != nil {
		fmt.Println("failed to read file:", err)
		os.Exit(1)
	}

	var policy models.PIIPolicy
	if err := json.Unmarshal(data, &policy); err != nil {
		fmt.Println("failed to unmarshal:", err)
		os.Exit(1)
	}

	specBytes, err := json.Marshal(policy.Spec)
	if err != nil {
		fmt.Println("failed to marshal spec:", err)
		os.Exit(1)
	}

	hash := sha256.Sum256(specBytes)
	computed := hex.EncodeToString(hash[:])

	fmt.Println("Current checksum in file:", policy.Metadata.Checksum)
	fmt.Println("Computed checksum:       ", computed)

	if computed == policy.Metadata.Checksum {
		fmt.Println("MATCH — checksum is valid")
	} else {
		fmt.Println("MISMATCH — update the checksum field to:", computed)
	}
}
