// Package seed fills an empty registry with Multica's own starter templates:
// the "@multica" official source and one listing per package embedded under
// templates/, each approved, published, public, and targeted at every Multica
// workspace. Operators can then disable, retarget or supersede them from the
// console like any other listing.
//
// Apply is idempotent per (name, version): a version that already exists is
// skipped, and targeting is only set when the listing is created, so an
// operator's later changes survive a re-run.
package seed

import (
	"archive/zip"
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/registry"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

//go:embed all:templates
var templates embed.FS

// Namespace is the source the starter templates are published under.
const Namespace = "@multica"

// DisplayName is the source's label in the console.
const DisplayName = "Multica starter templates"

// Template is one embedded package, zipped the way an operator would upload it.
type Template struct {
	Dir      string
	Name     string
	Version  string
	Category string
	Archive  []byte
}

// Templates returns the embedded packages, parsed and validated.
func Templates() ([]Template, error) {
	entries, err := fs.ReadDir(templates, "templates")
	if err != nil {
		return nil, err
	}
	out := make([]Template, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		template, err := load(entry.Name())
		if err != nil {
			return nil, fmt.Errorf("seed template %s: %w", entry.Name(), err)
		}
		out = append(out, template)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func load(dir string) (Template, error) {
	root := "templates/" + dir
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	err := fs.WalkDir(templates, root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		content, err := fs.ReadFile(templates, path)
		if err != nil {
			return err
		}
		file, err := writer.Create(strings.TrimPrefix(path, root+"/"))
		if err != nil {
			return err
		}
		_, err = file.Write(content)
		return err
	})
	if err != nil {
		return Template{}, err
	}
	if err := writer.Close(); err != nil {
		return Template{}, err
	}
	pkg, err := registry.ParseArchive(buffer.Bytes())
	if err != nil {
		return Template{}, err
	}
	var meta struct {
		Category string `json:"category"`
	}
	if raw, ok := pkg.Manifest.Metadata["registry"]; ok {
		if err := json.Unmarshal(raw, &meta); err != nil {
			return Template{}, fmt.Errorf("metadata.registry: %w", err)
		}
	}
	return Template{Dir: dir, Name: pkg.Manifest.Name, Version: pkg.Manifest.Version, Category: meta.Category, Archive: buffer.Bytes()}, nil
}

// TxStarter opens the transactions Apply writes in; *pgxpool.Pool satisfies it.
type TxStarter interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Result says what Apply did, by "<name>@<version>".
type Result struct {
	Created []string
	Skipped []string
}

// Apply seeds the registry. actor is recorded as the operator on the rows it
// creates; pass a zero value for an unattended run.
func Apply(ctx context.Context, tx TxStarter, queries *db.Queries, svc *registry.Service, actor pgtype.UUID) (Result, error) {
	list, err := Templates()
	if err != nil {
		return Result{}, err
	}
	var result Result
	for _, template := range list {
		created, err := apply(ctx, tx, queries, svc, actor, template)
		if err != nil {
			return result, fmt.Errorf("seed %s@%s: %w", template.Name, template.Version, err)
		}
		label := template.Name + "@" + template.Version
		if created {
			result.Created = append(result.Created, label)
		} else {
			result.Skipped = append(result.Skipped, label)
		}
	}
	return result, nil
}

func apply(ctx context.Context, tx TxStarter, queries *db.Queries, svc *registry.Service, actor pgtype.UUID, template Template) (bool, error) {
	if _, err := svc.VersionIDByName(ctx, Namespace, template.Name, template.Version); err == nil {
		return false, nil
	} else if !isNotFound(err) {
		return false, err
	}

	transaction, err := tx.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = transaction.Rollback(ctx) }()
	q := queries.WithTx(transaction)

	source, err := q.GetRegistrySourceByNamespace(ctx, Namespace)
	if errors.Is(err, pgx.ErrNoRows) {
		source, err = svc.CreateSource(ctx, q, actor, registry.SourceInput{Namespace: Namespace, Kind: "official", DisplayName: DisplayName})
	}
	if err != nil {
		return false, err
	}
	_, lookupErr := q.GetRegistryPackageBySourceName(ctx, db.GetRegistryPackageBySourceNameParams{SourceID: source.ID, Name: template.Name})
	isNew := errors.Is(lookupErr, pgx.ErrNoRows)
	if lookupErr != nil && !isNew {
		return false, lookupErr
	}

	upload, err := svc.UploadVersion(ctx, q, actor, registry.UploadInput{SourceID: uuidString(source.ID), Archive: template.Archive, Category: template.Category})
	if err != nil {
		return false, err
	}
	if _, err := svc.ReviewVersion(ctx, q, actor, upload.VersionID, registry.ReviewApproved, "starter template"); err != nil {
		return false, err
	}
	if isNew {
		if _, err := svc.SetListingStatus(ctx, q, upload.ListingID, registry.StatusPublished, true); err != nil {
			return false, err
		}
		if err := svc.ReplaceTargeting(ctx, q, actor, upload.ListingID, []registry.Target{{Consumer: registry.ConsumerMultica}}); err != nil {
			return false, err
		}
	}
	return true, transaction.Commit(ctx)
}

func isNotFound(err error) bool {
	var regErr *registry.Error
	return errors.As(err, &regErr) && regErr.Kind == registry.ErrNotFound
}

func uuidString(id pgtype.UUID) string { return util.UUIDToString(id) }
