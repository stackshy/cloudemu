//go:build unix

package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
)

// runCost fetches and prints the estimated monthly cost of the current
// inventory. Flags: --json (raw), --home <dir>.
func runCost(args []string) error {
	home, rest := splitHomeFlag(args)

	jsonOut := false

	for _, a := range rest {
		if a == flagJSON {
			jsonOut = true
		}
	}

	dir, err := runDir(home)
	if err != nil {
		return err
	}

	base, err := adminBaseURL(dir)
	if err != nil {
		return err
	}

	body, err := netGET(base, "cost", url.Values{})
	if err != nil {
		return err
	}

	if jsonOut {
		fmt.Println(string(body))

		return nil
	}

	return printCost(body)
}

func printCost(body []byte) error {
	var resp struct {
		EstimatedMonthlyUSD float64            `json:"estimatedMonthlyUsd"`
		ByService           map[string]float64 `json:"byService"`
		Resources           []struct {
			Provider string `json:"provider"`
		} `json:"resources"`
	}

	if err := json.Unmarshal(body, &resp); err != nil {
		return err
	}

	if len(resp.ByService) == 0 {
		fmt.Println("no always-on resources to estimate (usage-based services are not priced)")

		return nil
	}

	keys := make([]string, 0, len(resp.ByService))
	for k := range resp.ByService {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	fmt.Printf("%-24s %12s\n", "PROVIDER/SERVICE", "EST. MONTHLY")

	for _, k := range keys {
		fmt.Printf("%-24s %11s%.2f\n", k, "$", resp.ByService[k])
	}

	fmt.Printf("%-24s %11s%.2f\n", "TOTAL (always-on)", "$", resp.EstimatedMonthlyUSD)
	fmt.Println("note: rough estimate of always-on resources; usage-based services (storage ops, DB throughput) are excluded")

	return nil
}
