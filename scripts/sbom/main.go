// Command sbom writes the software bill of materials for a release, in SPDX
// 2.3 JSON: what's inside the lf program and how each part was made. Laser
// Feet uses no third-party code, so the parts are the program itself, the Go
// toolchain and standard library it was built with, and the files in dist/.
//
//	go run ./scripts/sbom <version> <dist folder>
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: go run ./scripts/sbom <version> <dist folder>")
		os.Exit(2)
	}
	version, dist := os.Args[1], os.Args[2]
	created := time.Now().UTC().Format(time.RFC3339)
	if epoch := os.Getenv("SOURCE_DATE_EPOCH"); epoch != "" { // reproducible
		var sec int64
		fmt.Sscan(epoch, &sec)
		created = time.Unix(sec, 0).UTC().Format(time.RFC3339)
	}
	goVersion := strings.TrimPrefix(runtime.Version(), "go")
	repo := "https://github.com/billionairedev24/laserfeet"
	pkgs := []map[string]any{
		{
			"SPDXID": "SPDXRef-laserfeet", "name": "laserfeet", "versionInfo": version,
			"downloadLocation": "git+" + repo + "@v" + version, "licenseConcluded": "NOASSERTION",
			"supplier": "Organization: Laser Feet", "copyrightText": "NOASSERTION",
			"externalRefs":  []map[string]string{{"referenceCategory": "PACKAGE-MANAGER", "referenceType": "purl", "referenceLocator": "pkg:github/billionairedev24/laserfeet@v" + version}},
			"filesAnalyzed": false,
		},
		{
			"SPDXID": "SPDXRef-go", "name": "go", "versionInfo": goVersion,
			"downloadLocation": "https://go.dev/dl/", "licenseConcluded": "BSD-3-Clause",
			"supplier": "Organization: Google LLC", "copyrightText": "NOASSERTION",
			"externalRefs":  []map[string]string{{"referenceCategory": "PACKAGE-MANAGER", "referenceType": "purl", "referenceLocator": "pkg:golang/stdlib@" + goVersion}},
			"filesAnalyzed": false, "comment": "the Go toolchain and standard library: Laser Feet's only dependency",
		},
	}
	rels := []map[string]string{
		{"spdxElementId": "SPDXRef-DOCUMENT", "relationshipType": "DESCRIBES", "relatedSpdxElement": "SPDXRef-laserfeet"},
		{"spdxElementId": "SPDXRef-laserfeet", "relationshipType": "DEPENDS_ON", "relatedSpdxElement": "SPDXRef-go"},
	}
	var files []map[string]any
	names, _ := filepath.Glob(filepath.Join(dist, "*"))
	sort.Strings(names)
	for i, p := range names {
		base := filepath.Base(p)
		if base == "SHA256SUMS" || strings.HasSuffix(base, ".spdx.json") {
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		sum := sha256.Sum256(b)
		id := fmt.Sprintf("SPDXRef-File-%d", i)
		files = append(files, map[string]any{
			"SPDXID": id, "fileName": "./" + base,
			"checksums":        []map[string]string{{"algorithm": "SHA256", "checksumValue": hex.EncodeToString(sum[:])}},
			"licenseConcluded": "NOASSERTION", "copyrightText": "NOASSERTION",
		})
		rels = append(rels, map[string]string{"spdxElementId": "SPDXRef-laserfeet", "relationshipType": "CONTAINS", "relatedSpdxElement": id})
	}
	doc := map[string]any{
		"spdxVersion": "SPDX-2.3", "dataLicense": "CC0-1.0", "SPDXID": "SPDXRef-DOCUMENT",
		"name": "laserfeet-" + version, "documentNamespace": repo + "/releases/v" + version + "/sbom",
		"creationInfo": map[string]any{"created": created, "creators": []string{"Tool: laserfeet-scripts-sbom"}},
		"packages":     pkgs, "files": files, "relationships": rels,
	}
	out, _ := json.MarshalIndent(doc, "", "  ")
	if err := os.WriteFile(filepath.Join(dist, "laserfeet.spdx.json"), append(out, '\n'), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
