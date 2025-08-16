package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log"
)

func main() {
	// Generate Ed25519 key pair
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		log.Fatal(err)
	}
	
	// Base64 encode keys
	publicB64 := base64.StdEncoding.EncodeToString(publicKey)
	privateB64 := base64.StdEncoding.EncodeToString(privateKey)
	
	fmt.Printf("Public key:  %s\n", publicB64)
	fmt.Printf("Private key: %s\n", privateB64)
}