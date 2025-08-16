package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"strings"
)

func main() {
	if len(os.Args) != 4 {
		fmt.Println("Usage: go run verify_test.go <pubkey_file> <content_file> <signature_file>")
		os.Exit(1)
	}

	pubkeyFile := os.Args[1]
	contentFile := os.Args[2]
	sigFile := os.Args[3]

	// Read public key
	pubkeyBytes, err := ioutil.ReadFile(pubkeyFile)
	if err != nil {
		log.Fatalf("Failed to read public key: %v", err)
	}
	pubkeyB64 := strings.TrimSpace(string(pubkeyBytes))
	
	fmt.Printf("Public key (base64): %s\n", pubkeyB64)
	fmt.Printf("Public key length: %d\n", len(pubkeyB64))

	// Decode public key
	pubkey, err := base64.StdEncoding.DecodeString(pubkeyB64)
	if err != nil {
		log.Fatalf("Failed to decode public key: %v", err)
	}
	
	fmt.Printf("Decoded public key length: %d (expected: %d)\n", len(pubkey), ed25519.PublicKeySize)

	// Read content
	content, err := ioutil.ReadFile(contentFile)
	if err != nil {
		log.Fatalf("Failed to read content: %v", err)
	}
	
	fmt.Printf("Content length: %d bytes\n", len(content))

	// Read signature
	sigBytes, err := ioutil.ReadFile(sigFile)
	if err != nil {
		log.Fatalf("Failed to read signature: %v", err)
	}
	sigB64 := strings.TrimSpace(string(sigBytes))
	
	fmt.Printf("Signature (base64): %s\n", sigB64)
	fmt.Printf("Signature length: %d\n", len(sigB64))

	// Decode signature
	signature, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		log.Fatalf("Failed to decode signature: %v", err)
	}
	
	fmt.Printf("Decoded signature length: %d (expected: %d)\n", len(signature), ed25519.SignatureSize)

	// Verify signature
	valid := ed25519.Verify(pubkey, content, signature)
	fmt.Printf("Signature valid: %v\n", valid)
}