package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"companion-server/internal/assetbundle"
)

func main() {
	if len(os.Args) < 2 {
		fatalf("usage: assetbundle <pack|validate> [flags]")
	}
	switch os.Args[1] {
	case "pack":
		pack(os.Args[2:])
	case "validate":
		validate(os.Args[2:])
	default:
		fatalf("unknown command %q", os.Args[1])
	}
}

func pack(args []string) {
	fs := flag.NewFlagSet("pack", flag.ExitOnError)
	manifestPath := fs.String("manifest", "", "unsigned manifest JSON")
	assetsDir := fs.String("assets-dir", ".", "directory containing manifest asset paths")
	privateKeyPath := fs.String("private-key-file", "", "file containing base64url Ed25519 private key")
	keyID := fs.String("key-id", "", "trusted signing key identifier")
	outPath := fs.String("out", "", "output .zip bundle")
	_ = fs.Parse(args)
	if *manifestPath == "" || *privateKeyPath == "" || *keyID == "" || *outPath == "" {
		fatalf("pack requires -manifest, -private-key-file, -key-id and -out")
	}
	manifestRaw, err := os.ReadFile(*manifestPath)
	if err != nil {
		fatalf("read manifest: %v", err)
	}
	var m assetbundle.Manifest
	dec := json.NewDecoder(strings.NewReader(string(manifestRaw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		fatalf("decode manifest: %v", err)
	}
	files := make(map[string][]byte, len(m.Assets))
	for _, a := range m.Assets {
		if !safeAssetPath(a.Path) {
			fatalf("unsafe manifest asset path %q", a.Path)
		}
		data, err := os.ReadFile(filepath.Join(*assetsDir, filepath.FromSlash(a.Path)))
		if err != nil {
			fatalf("read asset %q: %v", a.Path, err)
		}
		files[a.Path] = data
	}
	priv := readKey(*privateKeyPath, ed25519.PrivateKeySize)
	raw, err := assetbundle.Pack(m, files, *keyID, ed25519.PrivateKey(priv))
	if err != nil {
		fatalf("pack: %v", err)
	}
	if err := os.WriteFile(*outPath, raw, 0o644); err != nil {
		fatalf("write bundle: %v", err)
	}
	sum := sha256.Sum256(raw)
	fmt.Printf("bundle=%s bytes=%d sha256=%s\n", *outPath, len(raw), hex.EncodeToString(sum[:]))
}

func validate(args []string) {
	fs := flag.NewFlagSet("validate", flag.ExitOnError)
	bundlePath := fs.String("bundle", "", "bundle .zip")
	publicKeyPath := fs.String("public-key-file", "", "file containing base64url Ed25519 public key")
	keyID := fs.String("key-id", "", "trusted signing key identifier")
	board := fs.String("board", "", "target board identifier")
	width := fs.Int("width", 0, "target display width")
	height := fs.Int("height", 0, "target display height")
	abi := fs.Int("abi", 0, "target asset ABI")
	_ = fs.Parse(args)
	if *bundlePath == "" || *publicKeyPath == "" || *keyID == "" || *board == "" || *width <= 0 || *height <= 0 || *abi <= 0 {
		fatalf("validate requires -bundle, -public-key-file, -key-id, -board, positive -width/-height and -abi")
	}
	raw, err := os.ReadFile(*bundlePath)
	if err != nil {
		fatalf("read bundle: %v", err)
	}
	pub := readKey(*publicKeyPath, ed25519.PublicKeySize)
	report, err := assetbundle.Validate(raw, assetbundle.ValidateOptions{
		Board: *board, Width: *width, Height: *height, AssetABI: *abi,
		TrustedKeys: map[string]ed25519.PublicKey{*keyID: ed25519.PublicKey(pub)},
	})
	if err != nil {
		fatalf("validate: %v", err)
	}
	fmt.Printf("valid bundle_id=%s version=%s assets=%d expanded_bytes=%d sha256=%s\n",
		report.Manifest.BundleID, report.Manifest.Version, len(report.Manifest.Assets), report.ExpandedBytes, report.ArchiveSHA256)
}

func readKey(filename string, want int) []byte {
	raw, err := os.ReadFile(filename)
	if err != nil {
		fatalf("read key: %v", err)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(decoded) != want {
		fatalf("key %s must contain %d base64url-decoded bytes", filename, want)
	}
	return decoded
}

func safeAssetPath(name string) bool {
	return name != "" && !strings.Contains(name, "\\") && !strings.HasPrefix(name, "/") &&
		path.Clean(name) == name && strings.HasPrefix(name, "assets/") && len(name) > len("assets/")
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(2)
}
