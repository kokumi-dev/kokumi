// Package scan scans the images Kokumi vendors for vulnerabilities. It looks
// for an SBOM it may reuse, has the scanner generate one otherwise and
// match it against the vulnerability database, signs the generated SBOM and
// the report, attaches them to the image, and returns a record of each scan.
// It also decides which images are in use and due. It has its own types and
// knows no Kubernetes objects.
package scan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/kokumi-dev/kokumi/internal/attestation"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/sbom"
	"github.com/kokumi-dev/kokumi/internal/scanner"
	"github.com/kokumi-dev/kokumi/internal/signing"
	"github.com/kokumi-dev/kokumi/internal/vulnerability"
)

// Record is the latest scan of an image digest.
type Record struct {
	Digest string
	// Repository is the oci:// repository of the image.
	Repository string
	// Platform is the platform scanned of an image index, "" for a
	// single-platform image.
	Platform  string
	ScannedAt time.Time
	SBOM      SBOM
	// Report is the digest of the signed report referrer.
	Report  string
	Summary vulnerability.Summary
}

// SBOM identifies the SBOM a scan was computed from.
type SBOM struct {
	// Digest is the referrer or attestation manifest that holds it.
	Digest string
	Origin sbom.Origin
}

// Scanner scans images. *scanner.Client implements it.
type Scanner interface {
	Scan(ctx context.Context, req scanner.Request) (*scanner.Response, error)
}

// Pipeline scans images and attaches the signed results to them.
type Pipeline struct {
	Scanner Scanner
	// Platform is the platform scanned of multi-platform images, the first
	// one when an image lacks it.
	Platform string
	// Signer signs the results.
	Signer *signing.Signer
	// Trust verifies the SBOMs that may be reused.
	Trust *signing.TrustSet
	// Timeout bounds one call of the scanner.
	Timeout time.Duration
}

// Target is images in one registry, scanned in one call of the scanner.
type Target struct {
	// Client reads the images and attaches the results.
	Client oci.Client
	// Connection is how the scanner connects to the registry.
	Connection scanner.Connection
	// Images are the images, by digest.
	Images []oci.Reference
}

// Result is the outcome of scanning a Target.
type Result struct {
	Scanner  scanner.ScannerInfo
	Database scanner.DatabaseInfo
	Records  []Record
	// Failures are the images that could not be scanned.
	Failures []Failure
}

// Failure is an image that could not be scanned.
type Failure struct {
	Digest string
	Err    error
}

// String returns "<digest>: <error>".
func (f Failure) String() string {
	return f.Digest + ": " + f.Err.Error()
}

// Run scans the images of target with dbs and attaches the signed results
// to them, recorded at now. An image that fails is a Failure of the Result;
// an error means the scanner could not be called, such as a
// *scanner.UnavailableError.
func (p Pipeline) Run(ctx context.Context, target Target, dbs Databases, now time.Time) (Result, error) {
	type prepared struct {
		target sbom.Target
		found  *sbom.Found
	}
	var result Result
	var images []prepared
	req := scanner.Request{Database: dbs.DB, JavaDatabase: dbs.JavaDB, Registry: target.Connection}
	for _, ref := range target.Images {
		selected, err := sbom.Select(ctx, target.Client, ref, p.Platform)
		if err != nil {
			result.Failures = append(result.Failures, Failure{Digest: ref.Digest, Err: err})
			continue
		}
		found, err := sbom.Find(ctx, target.Client, p.Trust, selected)
		if err != nil {
			result.Failures = append(result.Failures, Failure{Digest: ref.Digest, Err: fmt.Errorf("looking up SBOMs: %w", err)})
			continue
		}
		img := scanner.Image{Ref: selected.Image.OCIString(), Platform: selected.Platform}
		if found != nil {
			img.SBOM = found.Document
		}
		images = append(images, prepared{target: selected, found: found})
		req.Images = append(req.Images, img)
	}
	if len(req.Images) == 0 {
		return result, nil
	}

	scanCtx, cancel := context.WithTimeout(ctx, p.Timeout)
	defer cancel()
	resp, err := p.Scanner.Scan(scanCtx, req)
	if err != nil {
		return Result{}, err
	}

	result.Scanner, result.Database = resp.Scanner, resp.Database
	for i, scanned := range resp.Images {
		img := images[i]
		if scanned.Error != "" {
			result.Failures = append(result.Failures, Failure{Digest: img.target.Image.Digest, Err: errors.New(scanned.Error)})
			continue
		}
		record, err := p.attach(ctx, target.Client, img.target, img.found, scanned, resp.Database, now)
		if err != nil {
			result.Failures = append(result.Failures, Failure{Digest: img.target.Image.Digest, Err: err})
			continue
		}
		result.Records = append(result.Records, record)
	}
	return result, nil
}

// attach signs the results of one image and attaches them to it: the SBOM
// the scanner generated, unless one was reused, and the trimmed report,
// which names the SBOM it was computed from.
func (p Pipeline) attach(
	ctx context.Context, client oci.Client, target sbom.Target, found *sbom.Found,
	scanned scanner.Result, db scanner.DatabaseInfo, now time.Time,
) (Record, error) {
	subjectName := target.Image.RepositoryReference()
	annotations := map[string]string{}
	if target.Platform != "" {
		annotations[sbom.AnnotationPlatform] = target.Platform
	}

	var reference SBOM
	if found != nil {
		reference = SBOM{Digest: found.Digest, Origin: found.Origin}
	} else {
		if len(scanned.SBOM) == 0 {
			return Record{}, errors.New("the scanner returned no SBOM")
		}
		statement, err := attestation.NewStatement(subjectName, target.Image.Digest, sbom.PredicateType, scanned.SBOM)
		if err != nil {
			return Record{}, err
		}
		digest, err := attestation.PushAnnotated(ctx, client, p.Signer, target.Image, sbom.PredicateType, statement, now, annotations)
		if err != nil {
			return Record{}, fmt.Errorf("attaching SBOM: %w", err)
		}
		reference = SBOM{Digest: digest, Origin: sbom.OriginGenerated}
	}

	report, err := vulnerability.Parse(scanned.Report)
	if err != nil {
		return Record{}, err
	}
	if err := report.Trim(); err != nil {
		return Record{}, err
	}
	findings, err := report.Findings()
	if err != nil {
		return Record{}, err
	}
	report.Invocation.Parameters = vulnerability.Parameters{
		SBOM:     vulnerability.ParametersSBOM{Digest: reference.Digest, Origin: string(reference.Origin)},
		Platform: target.Platform,
	}
	report.Scanner.DB = vulnerability.DB{URI: db.Ref, Version: db.UpdatedAt.UTC().Format(time.RFC3339)}
	reportJSON, err := json.Marshal(report)
	if err != nil {
		return Record{}, fmt.Errorf("encoding report: %w", err)
	}
	statement, err := attestation.NewStatement(subjectName, target.Image.Digest, vulnerability.PredicateType, json.RawMessage(reportJSON))
	if err != nil {
		return Record{}, err
	}
	reportAnnotations := maps.Clone(annotations)
	reportAnnotations[sbom.AnnotationSBOMOrigin] = strings.ToLower(string(reference.Origin))
	reportDigest, err := attestation.PushAnnotated(ctx, client, p.Signer, target.Image, vulnerability.PredicateType, statement, now, reportAnnotations)
	if err != nil {
		return Record{}, fmt.Errorf("attaching report: %w", err)
	}

	return Record{
		Digest:     target.Image.Digest,
		Repository: target.Image.OCIRepositoryReference(),
		Platform:   target.Platform,
		ScannedAt:  now,
		SBOM:       reference,
		Report:     reportDigest,
		Summary:    vulnerability.Summarize(findings),
	}, nil
}
