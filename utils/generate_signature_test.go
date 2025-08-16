package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"io/ioutil"
	"log"
	"os"
)

func main() {
	if len(os.Args) != 4 {
		fmt.Println("Usage: go run generate_signature_test.go <private_key_b64> <content_file> <output_sig_file>")
		os.Exit(1)
	}

	privateKeyB64 := os.Args[1]
	contentFile := os.Args[2]
	outputSigFile := os.Args[3]

	// Decode private key
	privateKeyBytes, err := base64.StdEncoding.DecodeString(privateKeyB64)
	if err != nil {
		log.Fatalf("Failed to decode private key: %v", err)
	}

	if len(privateKeyBytes) != ed25519.PrivateKeySize {
		log.Fatalf("Invalid private key size: expected %d, got %d", ed25519.PrivateKeySize, len(privateKeyBytes))
	}

	privateKey := ed25519.PrivateKey(privateKeyBytes)
	
	// Extract and show public key from private key
	publicKey := privateKey.Public().(ed25519.PublicKey)
	publicKeyB64 := base64.StdEncoding.EncodeToString(publicKey)
	fmt.Printf("Public key from private key: %s\n", publicKeyB64)

	// Read content
	content, err := ioutil.ReadFile(contentFile)
	if err != nil {
		log.Fatalf("Failed to read content: %v", err)
	}

	// Sign content
	signature := ed25519.Sign(privateKey, content)

	// Base64 encode signature
	signatureB64 := base64.StdEncoding.EncodeToString(signature)

	// Write signature file
	if err := ioutil.WriteFile(outputSigFile, []byte(signatureB64), 0644); err != nil {
		log.Fatalf("Failed to write signature: %v", err)
	}

	fmt.Printf("Generated signature: %s\n", signatureB64)
	fmt.Printf("Signature written to: %s\n", outputSigFile)
}