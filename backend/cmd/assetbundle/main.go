package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"companion-server/internal/assetbundle"
)

func main() {
	if len(os.Args) < 3 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "inspect":
		err = inspect(os.Args[2:])
	case "validate":
		err = validate(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "assetbundle:", err)
		os.Exit(1)
	}
}

func inspect(args []string) error {
	fs := flag.NewFlagSet("inspect", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("inspect requires exactly one bundle path")
	}
	raw, err := readBundle(fs.Arg(0))
	if err != nil {
		return err
	}
	m, err := assetbundle.Inspect(raw, assetbundle.DefaultLimits())
	if err != nil {
		return err
	}
	out, _ := json.MarshalIndent(m, "", "  ")
	fmt.Println(string(out))
	return nil
}

func validate(args []string) error {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	publicKey := fs.String("public-key", "", "trusted Ed25519 public key (raw URL-safe base64)")
	board := fs.String("board", "", "target board family")
	width := fs.Int("display-width", 0, "target display width; 0 for unspecified")
	height := fs.Int("display-height", 0, "target display height; 0 for unspecified")
	abi := fs.Int("asset-abi", 0, "target asset ABI")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 || *publicKey == "" || *board == "" || *abi <= 0 {
		return fmt.Errorf("validate requires bundle path, -public-key, -board and positive -asset-abi")
	}
	keyBytes, err := base64.RawURLEncoding.DecodeString(*publicKey)
	if err != nil || len(keyBytes) != ed25519.PublicKeySize {
		return fmt.Errorf("public key must decode to %d Ed25519 bytes", ed25519.PublicKeySize)
	}
	raw, err := readBundle(fs.Arg(0))
	if err != nil {
		return err
	}
	result, err := assetbundle.Validate(raw, ed25519.PublicKey(keyBytes), assetbundle.ValidationTarget{Board: *board, DisplayWidth: *width, DisplayHeight: *height, AssetABI: *abi}, assetbundle.DefaultLimits())
	if err != nil {
		return err
	}
	fmt.Printf("PASS bundle_id=%s version=%s manifest_sha256=%s bundle_sha256=%s\n", result.Manifest.BundleID, result.Manifest.Version, result.ManifestSHA256, result.BundleSHA256)
	return nil
}

func readBundle(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > assetbundle.DefaultLimits().MaxBundleBytes {
		return nil, fmt.Errorf("bundle exceeds %d-byte host limit", assetbundle.DefaultLimits().MaxBundleBytes)
	}
	return os.ReadFile(path)
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: assetbundle <inspect|validate> [flags] BUNDLE")
	os.Exit(2)
}
