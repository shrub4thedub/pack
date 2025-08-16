package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Println("Usage: go run signer.go <private_key_b64> <file_or_directory>")
		os.Exit(1)
	}
	
	privateKeyB64 := os.Args[1]
	target := os.Args[2]
	
	// Decode private key
	privateKeyBytes, err := base64.StdEncoding.DecodeString(privateKeyB64)
	if err != nil {
		log.Fatalf("Failed to decode private key: %v", err)
	}
	
	if len(privateKeyBytes) != ed25519.PrivateKeySize {
		log.Fatalf("Invalid private key size: expected %d, got %d", ed25519.PrivateKeySize, len(privateKeyBytes))
	}
	
	privateKey := ed25519.PrivateKey(privateKeyBytes)
	
	// Check if target is directory or file
	stat, err := os.Stat(target)
	if err != nil {
		log.Fatalf("Failed to stat target: %v", err)
	}
	
	if stat.IsDir() {
		// Sign all .box files in directory
		err = filepath.Walk(target, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			
			if strings.HasSuffix(path, ".box") {
				return signFile(privateKey, path)
			}
			
			return nil
		})
		
		if err != nil {
			log.Fatalf("Failed to walk directory: %v", err)
		}
	} else {
		// Sign single file
		if err := signFile(privateKey, target); err != nil {
			log.Fatalf("Failed to sign file: %v", err)
		}
	}
}

func signFile(privateKey ed25519.PrivateKey, filePath string) error {
	// Read file content
	content, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read %s: %v", filePath, err)
	}
	
	// Sign content
	signature := ed25519.Sign(privateKey, content)
	
	// Base64 encode signature
	signatureB64 := base64.StdEncoding.EncodeToString(signature)
	
	// Write signature file
	sigPath := filePath + ".sig"
	if err := os.WriteFile(sigPath, []byte(signatureB64), 0644); err != nil {
		return fmt.Errorf("failed to write signature %s: %v", sigPath, err)
	}
	
	fmt.Printf("Signed: %s -> %s\n", filePath, sigPath)
	return nil
}