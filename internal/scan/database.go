package scan

import (
	"cmp"
	"context"
	"fmt"

	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/scanner"
)

// Tags the vulnerability databases are read at when their repository names
// none: their schema versions.
const (
	DBTag     = "2"
	JavaDBTag = "1"
)

// Databases are the vulnerability databases a scan uses, by digest.
type Databases struct {
	DB scanner.Database
	// JavaDB is nil when the Java database could not be resolved; images
	// without Java archives scan without it.
	JavaDB *scanner.Database
}

// Connector returns the client and the scanner's connection for a
// repository.
type Connector func(repository string) (oci.Client, scanner.Connection, error)

// ResolveDatabases resolves the repositories of the vulnerability database
// and the Java database to digests, at their schema tags unless the URLs
// name a tag or digest. A Java database that cannot be resolved is javaErr,
// and the Databases lack it.
func ResolveDatabases(ctx context.Context, connect Connector, dbURL, javaDBURL string) (dbs Databases, javaErr, err error) {
	if dbs.DB, err = resolve(ctx, connect, dbURL, DBTag); err != nil {
		return Databases{}, nil, fmt.Errorf("vulnerability database: %w", err)
	}
	javaDB, javaErr := resolve(ctx, connect, javaDBURL, JavaDBTag)
	if javaErr != nil {
		return dbs, fmt.Errorf("the Java database: %w", javaErr), nil
	}
	dbs.JavaDB = &javaDB
	return dbs, nil, nil
}

// resolve resolves the database repository url to a digest, at tag unless
// url names one.
func resolve(ctx context.Context, connect Connector, url, tag string) (scanner.Database, error) {
	ref, err := oci.Parse(url)
	if err != nil {
		return scanner.Database{}, err
	}
	client, conn, err := connect(ref.OCIRepositoryReference())
	if err != nil {
		return scanner.Database{}, err
	}
	if ref.Digest == "" {
		ref.Tag = cmp.Or(ref.Tag, tag)
		desc, err := client.Resolve(ctx, ref)
		if err != nil {
			return scanner.Database{}, fmt.Errorf("resolving %s: %w", ref.OCIString(), err)
		}
		ref.Digest = desc.Digest
	}
	ref.Tag = ""
	return scanner.Database{Ref: ref.OCIString(), Registry: conn}, nil
}
