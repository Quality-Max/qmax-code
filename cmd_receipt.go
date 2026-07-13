package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	receipt "github.com/Quality-Max/qmax-receipt"
)

// handleReceiptCommand implements `qmax-code receipt <list|show|verify> [id|latest]`
// — the customer-facing surface for the Exposure Receipt: "here is exactly what
// left your network this session, and it verifies offline." verify is fully
// offline and prints the provenance-not-honesty caveat.
func handleReceiptCommand(args []string) {
	initReceiptPaths()

	sub := "list"
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "list":
		receiptList()
	case "show":
		receiptShow(argAt(args, 1))
	case "verify":
		receiptVerify(argAt(args, 1))
	default:
		fmt.Fprintln(os.Stderr, "Usage: qmax-code receipt <list|show|verify> [id|latest]")
		os.Exit(1)
	}
}

func argAt(args []string, i int) string {
	if len(args) > i {
		return args[i]
	}
	return ""
}

func receiptList() {
	paths, err := receipt.List()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if len(paths) == 0 {
		fmt.Println("No exposure receipts found.")
		return
	}
	fmt.Printf("%-34s  %-20s  %-8s  %s\n", "RUN ID", "KIND", "REQUESTS", "DESTINATIONS")
	for _, p := range paths {
		r, err := receipt.Load(p)
		if err != nil {
			continue
		}
		fmt.Printf("%-34s  %-20s  %-8d  %v\n", r.RunID, r.RunKind, r.Summary.TotalRequests, r.Destinations)
	}
}

func receiptShow(idOrLatest string) {
	r := mustResolveReceipt(idOrLatest)
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot marshal receipt: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(data))
}

func receiptVerify(idOrLatest string) {
	r := mustResolveReceipt(idOrLatest)
	if err := receipt.Verify(r); err != nil {
		fmt.Fprintf(os.Stderr, "✗ INVALID: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ VALID — run %s (%s), %d request(s) across %v\n",
		r.RunID, r.RunKind, r.Summary.TotalRequests, r.Destinations)
	fmt.Println("Note: this proves the receipt was produced by this agent's key (provenance),")
	fmt.Println("not that the agent disclosed everything. Cross-check against your own egress logs.")
}

func mustResolveReceipt(idOrLatest string) *receipt.Receipt {
	dir, err := receipt.ReceiptsDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if idOrLatest == "" || idOrLatest == "latest" {
		paths, err := receipt.List()
		if err != nil || len(paths) == 0 {
			fmt.Fprintln(os.Stderr, "No exposure receipts found.")
			os.Exit(1)
		}
		r, err := receipt.Load(paths[len(paths)-1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return r
	}
	r, err := receipt.Load(filepath.Join(dir, idOrLatest+".json"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	return r
}
