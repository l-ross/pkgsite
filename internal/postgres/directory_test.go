// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/google/safehtml"
	"golang.org/x/pkgsite/internal"
	"golang.org/x/pkgsite/internal/derrors"
	"golang.org/x/pkgsite/internal/licenses"
	"golang.org/x/pkgsite/internal/source"
	"golang.org/x/pkgsite/internal/stdlib"
	"golang.org/x/pkgsite/internal/testing/sample"
)

func TestGetPackagesInDirectory(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	defer ResetTestDB(testDB, t)

	InsertSampleDirectoryTree(ctx, t, testDB)

	for _, tc := range []struct {
		name, dirPath, modulePath, version, wantModulePath, wantVersion string
		wantSuffixes                                                    []string
		wantNotFoundErr                                                 bool
	}{
		{
			name:           "directory path is the module path",
			dirPath:        "github.com/hashicorp/vault",
			modulePath:     "github.com/hashicorp/vault",
			version:        "v1.2.3",
			wantVersion:    "v1.2.3",
			wantModulePath: "github.com/hashicorp/vault",
			wantSuffixes: []string{
				"builtin/audit/file",
				"builtin/audit/socket",
				"internal/foo",
				"vault/replication",
				"vault/seal/transit",
			},
		},
		{
			name:           "directory path is a package path",
			dirPath:        "github.com/hashicorp/vault",
			modulePath:     "github.com/hashicorp/vault",
			version:        "v1.0.3",
			wantVersion:    "v1.0.3",
			wantModulePath: "github.com/hashicorp/vault",
			wantSuffixes: []string{
				"api",
				"builtin/audit/file",
				"builtin/audit/socket",
			},
		},
		{
			name:           "directory path is not a package or module",
			dirPath:        "github.com/hashicorp/vault/builtin",
			modulePath:     "github.com/hashicorp/vault",
			wantModulePath: "github.com/hashicorp/vault",
			version:        "v1.0.3",
			wantVersion:    "v1.0.3",
			wantSuffixes: []string{
				"builtin/audit/file",
				"builtin/audit/socket",
			},
		},
		{
			name:           "stdlib module",
			dirPath:        stdlib.ModulePath,
			modulePath:     stdlib.ModulePath,
			version:        "v1.13.4",
			wantModulePath: stdlib.ModulePath,
			wantVersion:    "v1.13.4",
			wantSuffixes: []string{
				"archive/tar",
				"archive/zip",
				"cmd/go",
				"cmd/internal/obj",
				"cmd/internal/obj/arm",
				"cmd/internal/obj/arm64",
			},
		},
		{
			name:           "stdlib directory",
			dirPath:        "archive",
			modulePath:     stdlib.ModulePath,
			version:        "v1.13.4",
			wantModulePath: stdlib.ModulePath,
			wantVersion:    "v1.13.4",
			wantSuffixes: []string{
				"archive/tar",
				"archive/zip",
			},
		},
		{
			name:           "stdlib package",
			dirPath:        "archive/zip",
			modulePath:     stdlib.ModulePath,
			version:        "v1.13.4",
			wantModulePath: stdlib.ModulePath,
			wantVersion:    "v1.13.4",
			wantSuffixes: []string{
				"archive/zip",
			},
		},
		{
			name:            "stdlib package -  incomplete last element",
			dirPath:         "archive/zi",
			modulePath:      stdlib.ModulePath,
			version:         "v1.13.4",
			wantNotFoundErr: true,
		},
		{
			name:           "stdlib - internal directory",
			dirPath:        "cmd/internal",
			modulePath:     stdlib.ModulePath,
			version:        "v1.13.4",
			wantModulePath: stdlib.ModulePath,
			wantVersion:    "v1.13.4",
			wantSuffixes: []string{
				"cmd/internal/obj",
				"cmd/internal/obj/arm",
				"cmd/internal/obj/arm64",
			},
		},
		{
			name:           "stdlib - directory nested within an internal directory",
			dirPath:        "cmd/internal/obj",
			modulePath:     stdlib.ModulePath,
			version:        "v1.13.4",
			wantModulePath: stdlib.ModulePath,
			wantVersion:    "v1.13.4",
			wantSuffixes: []string{
				"cmd/internal/obj",
				"cmd/internal/obj/arm",
				"cmd/internal/obj/arm64",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := testDB.GetPackagesInDirectory(ctx, tc.dirPath, tc.modulePath, tc.version)
			if tc.wantNotFoundErr {
				if !errors.Is(err, derrors.NotFound) {
					t.Fatalf("got error %v; want %v", err, derrors.NotFound)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}

			var wantPackages []*internal.PackageMeta
			for _, suffix := range tc.wantSuffixes {
				pkg := sample.PackageMeta(tc.wantModulePath, suffix)
				wantPackages = append(wantPackages, pkg)
			}

			opts := []cmp.Option{
				// The packages table only includes partial license information; it omits the Coverage field.
				cmpopts.IgnoreFields(licenses.Metadata{}, "Coverage"),
			}
			if diff := cmp.Diff(wantPackages, got, opts...); diff != "" {
				t.Errorf("mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestLegacyGetDirectory(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	defer ResetTestDB(testDB, t)

	InsertSampleDirectoryTree(ctx, t, testDB)

	for _, tc := range []struct {
		name, dirPath, modulePath, version, wantModulePath, wantVersion string
		wantSuffixes                                                    []string
		wantNotFoundErr                                                 bool
	}{
		{
			name:           "latest with ambigious module path, should match longest module path",
			dirPath:        "github.com/hashicorp/vault/api",
			modulePath:     internal.UnknownModulePath,
			version:        internal.LatestVersion,
			wantVersion:    "v1.1.2",
			wantModulePath: "github.com/hashicorp/vault/api",
			wantSuffixes:   []string{""},
		},
		{
			name:           "specified version with ambigious module path, should match longest module path",
			dirPath:        "github.com/hashicorp/vault/api",
			modulePath:     internal.UnknownModulePath,
			version:        "v1.1.2",
			wantVersion:    "v1.1.2",
			wantModulePath: "github.com/hashicorp/vault/api",
			wantSuffixes:   []string{""},
		},
		{
			name:           "specified version with ambigous module path, but only shorter module path matches for specified version",
			dirPath:        "github.com/hashicorp/vault/api",
			modulePath:     internal.UnknownModulePath,
			version:        "v1.0.3",
			wantVersion:    "v1.0.3",
			wantModulePath: "github.com/hashicorp/vault",
			wantSuffixes:   []string{"api"},
		},
		{
			name:           "specified version with ambiguous module path, two module versions exist, but only shorter module path contains matching package",
			dirPath:        "github.com/hashicorp/vault/builtin/audit",
			modulePath:     internal.UnknownModulePath,
			version:        "v1.1.2",
			wantVersion:    "v1.1.2",
			wantModulePath: "github.com/hashicorp/vault",
			wantSuffixes: []string{
				"builtin/audit/file",
				"builtin/audit/socket",
			},
		},
		{
			name:           "specified module path and version, should match specified shorter module path",
			dirPath:        "github.com/hashicorp/vault/api",
			modulePath:     "github.com/hashicorp/vault",
			version:        "v1.0.3",
			wantVersion:    "v1.0.3",
			wantModulePath: "github.com/hashicorp/vault",
			wantSuffixes:   []string{"api"},
		},
		{
			name:           "directory path is the module path at latest",
			dirPath:        "github.com/hashicorp/vault",
			modulePath:     "github.com/hashicorp/vault",
			version:        internal.LatestVersion,
			wantVersion:    "v1.2.3",
			wantModulePath: "github.com/hashicorp/vault",
			wantSuffixes: []string{
				"builtin/audit/file",
				"builtin/audit/socket",
				"internal/foo",
				"vault/replication",
				"vault/seal/transit",
			},
		},
		{
			name:           "directory path is the module path with specified version",
			dirPath:        "github.com/hashicorp/vault",
			modulePath:     "github.com/hashicorp/vault",
			version:        "v1.0.3",
			wantVersion:    "v1.0.3",
			wantModulePath: "github.com/hashicorp/vault",
			wantSuffixes: []string{
				"api",
				"builtin/audit/file",
				"builtin/audit/socket",
			},
		},
		{
			name:           "directory path is a package path",
			dirPath:        "github.com/hashicorp/vault",
			modulePath:     "github.com/hashicorp/vault",
			version:        "v1.0.3",
			wantVersion:    "v1.0.3",
			wantModulePath: "github.com/hashicorp/vault",
			wantSuffixes: []string{
				"api",
				"builtin/audit/file",
				"builtin/audit/socket",
			},
		},
		{
			name:           "valid directory path with package at version, no module path",
			dirPath:        "github.com/hashicorp/vault/builtin",
			modulePath:     internal.UnknownModulePath,
			wantModulePath: "github.com/hashicorp/vault",
			version:        "v1.0.3",
			wantVersion:    "v1.0.3",
			wantSuffixes: []string{
				"builtin/audit/file",
				"builtin/audit/socket",
			},
		},
		{
			name:           "valid directory path with package, specified version and module path",
			dirPath:        "github.com/hashicorp/vault/builtin",
			modulePath:     "github.com/hashicorp/vault",
			wantModulePath: "github.com/hashicorp/vault",
			version:        "v1.0.3",
			wantVersion:    "v1.0.3",
			wantSuffixes: []string{
				"builtin/audit/file",
				"builtin/audit/socket",
			},
		},
		{
			name:           "latest version of github.com/hashicorp/vault/api in github.com/hashicorp/vault",
			dirPath:        "github.com/hashicorp/vault/api",
			modulePath:     "github.com/hashicorp/vault",
			version:        internal.LatestVersion,
			wantModulePath: "github.com/hashicorp/vault",
			wantVersion:    "v1.1.2",
			wantSuffixes: []string{
				"api",
			},
		},
		{
			name:           "latest version of github.com/hashicorp/vault/api in github.com/hashicorp/vault/api",
			dirPath:        "github.com/hashicorp/vault/api",
			modulePath:     "github.com/hashicorp/vault/api",
			version:        internal.LatestVersion,
			wantModulePath: "github.com/hashicorp/vault/api",
			wantVersion:    "v1.1.2",
			wantSuffixes:   []string{""},
		},
		{
			name:           "latest version of internal directory in github.com/hashicorp/vault",
			dirPath:        "github.com/hashicorp/vault/internal",
			modulePath:     internal.UnknownModulePath,
			version:        internal.LatestVersion,
			wantModulePath: "github.com/hashicorp/vault",
			wantVersion:    "v1.2.3",
			wantSuffixes:   []string{"internal/foo"},
		},
		{
			name:            "invalid directory, incomplete last element",
			dirPath:         "github.com/hashicorp/vault/builti",
			modulePath:      internal.UnknownModulePath,
			version:         "v1.0.3",
			wantNotFoundErr: true,
		},
		{
			name:           "stdlib directory",
			dirPath:        "archive",
			modulePath:     stdlib.ModulePath,
			version:        internal.LatestVersion,
			wantModulePath: stdlib.ModulePath,
			wantVersion:    "v1.13.4",
			wantSuffixes: []string{
				"archive/tar",
				"archive/zip",
			},
		},
		{
			name:           "stdlib package",
			dirPath:        "archive/zip",
			modulePath:     stdlib.ModulePath,
			version:        internal.LatestVersion,
			wantModulePath: stdlib.ModulePath,
			wantVersion:    "v1.13.4",
			wantSuffixes: []string{
				"archive/zip",
			},
		},
		{
			name:            "stdlib package -  incomplete last element",
			dirPath:         "archive/zi",
			modulePath:      stdlib.ModulePath,
			version:         internal.LatestVersion,
			wantNotFoundErr: true,
		},
		{
			name:           "stdlib - internal directory",
			dirPath:        "cmd/internal",
			modulePath:     stdlib.ModulePath,
			version:        internal.LatestVersion,
			wantModulePath: stdlib.ModulePath,
			wantVersion:    "v1.13.4",
			wantSuffixes: []string{
				"cmd/internal/obj",
				"cmd/internal/obj/arm",
				"cmd/internal/obj/arm64",
			},
		},
		{
			name:           "stdlib - directory nested within an internal directory",
			dirPath:        "cmd/internal/obj",
			modulePath:     stdlib.ModulePath,
			version:        internal.LatestVersion,
			wantModulePath: stdlib.ModulePath,
			wantVersion:    "v1.13.4",
			wantSuffixes: []string{
				"cmd/internal/obj",
				"cmd/internal/obj/arm",
				"cmd/internal/obj/arm64",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := testDB.LegacyGetDirectory(ctx, tc.dirPath, tc.modulePath, tc.version, internal.AllFields)
			if tc.wantNotFoundErr {
				if !errors.Is(err, derrors.NotFound) {
					t.Fatalf("got error %v; want %v", err, derrors.NotFound)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}

			mi := sample.LegacyModuleInfo(tc.wantModulePath, tc.wantVersion)
			var wantPackages []*internal.LegacyPackage
			for _, suffix := range tc.wantSuffixes {
				pkg := sample.LegacyPackage(tc.wantModulePath, suffix)
				pkg.Imports = nil
				wantPackages = append(wantPackages, pkg)
			}

			wantDirectory := &internal.LegacyDirectory{
				LegacyModuleInfo: *mi,
				Packages:         wantPackages,
				Path:             tc.dirPath,
			}
			opts := []cmp.Option{
				cmp.AllowUnexported(source.Info{}, safehtml.HTML{}),
				// The packages table only includes partial license information; it omits the Coverage field.
				cmpopts.IgnoreFields(licenses.Metadata{}, "Coverage"),
			}
			if diff := cmp.Diff(wantDirectory, got, opts...); diff != "" {
				t.Errorf("testDB.LegacyGetDirectory(ctx, %q, %q, %q) mismatch (-want +got):\n%s", tc.dirPath, tc.modulePath, tc.version, diff)
			}
		})
	}
}

func TestGetDirectory(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	defer ResetTestDB(testDB, t)
	InsertSampleDirectoryTree(ctx, t, testDB)

	// Add a module that has READMEs in a directory and a package.
	m := sample.Module("a.com/m", "v1.2.3", "dir/p")
	d := findDirectory(m, "a.com/m/dir")
	d.Readme = &internal.Readme{
		Filepath: "DIR_README.md",
		Contents: "dir readme",
	}
	d = findDirectory(m, "a.com/m/dir/p")
	d.Readme = &internal.Readme{
		Filepath: "PKG_README.md",
		Contents: "pkg readme",
	}
	if err := testDB.InsertModule(ctx, m); err != nil {
		t.Fatal(err)
	}

	newVdir := func(path, modulePath, version string, readme *internal.Readme, pkg *internal.Package) *internal.VersionedDirectory {
		return &internal.VersionedDirectory{
			ModuleInfo: *sample.ModuleInfo(modulePath, version),
			Directory: internal.Directory{
				DirectoryMeta: internal.DirectoryMeta{
					Path:              path,
					V1Path:            path,
					IsRedistributable: true,
					Licenses:          sample.LicenseMetadata,
				},
				Readme:  readme,
				Package: pkg,
			},
		}
	}

	newPackage := func(name, path string) *internal.Package {
		return &internal.Package{
			Name: name,
			Path: path,
			Documentation: &internal.Documentation{
				Synopsis: sample.Synopsis,
				HTML:     sample.DocumentationHTML,
				GOOS:     sample.GOOS,
				GOARCH:   sample.GOARCH,
			},
			Imports: sample.Imports,
		}
	}

	for _, tc := range []struct {
		name, dirPath, modulePath, version string
		want                               *internal.VersionedDirectory
		wantNotFoundErr                    bool
	}{
		{
			name:       "module path",
			dirPath:    "github.com/hashicorp/vault",
			modulePath: "github.com/hashicorp/vault",
			version:    "v1.0.3",
			want: newVdir("github.com/hashicorp/vault", "github.com/hashicorp/vault", "v1.0.3",
				&internal.Readme{
					Filepath: sample.ReadmeFilePath,
					Contents: sample.ReadmeContents,
				}, nil),
		},
		{
			name:       "package path",
			dirPath:    "github.com/hashicorp/vault/api",
			modulePath: "github.com/hashicorp/vault",
			version:    "v1.0.3",
			want: newVdir("github.com/hashicorp/vault/api", "github.com/hashicorp/vault", "v1.0.3", nil,
				newPackage("api", "github.com/hashicorp/vault/api")),
		},
		{
			name:       "directory path",
			dirPath:    "github.com/hashicorp/vault/builtin",
			modulePath: "github.com/hashicorp/vault",
			version:    "v1.0.3",
			want:       newVdir("github.com/hashicorp/vault/builtin", "github.com/hashicorp/vault", "v1.0.3", nil, nil),
		},
		{
			name:       "stdlib directory",
			dirPath:    "archive",
			modulePath: stdlib.ModulePath,
			version:    "v1.13.4",
			want:       newVdir("archive", stdlib.ModulePath, "v1.13.4", nil, nil),
		},
		{
			name:       "stdlib package",
			dirPath:    "archive/zip",
			modulePath: stdlib.ModulePath,
			version:    "v1.13.4",
			want:       newVdir("archive/zip", stdlib.ModulePath, "v1.13.4", nil, newPackage("zip", "archive/zip")),
		},
		{
			name:            "stdlib package - incomplete last element",
			dirPath:         "archive/zi",
			modulePath:      stdlib.ModulePath,
			version:         "v1.13.4",
			wantNotFoundErr: true,
		},
		{
			name:       "stdlib - internal directory",
			dirPath:    "cmd/internal",
			modulePath: stdlib.ModulePath,
			version:    "v1.13.4",
			want:       newVdir("cmd/internal", stdlib.ModulePath, "v1.13.4", nil, nil),
		},
		{
			name:       "directory with readme",
			dirPath:    "a.com/m/dir",
			modulePath: "a.com/m",
			version:    "v1.2.3",
			want: newVdir("a.com/m/dir", "a.com/m", "v1.2.3", &internal.Readme{
				Filepath: "DIR_README.md",
				Contents: "dir readme",
			}, nil),
		},
		{
			name:       "package with readme",
			dirPath:    "a.com/m/dir/p",
			modulePath: "a.com/m",
			version:    "v1.2.3",
			want: newVdir("a.com/m/dir/p", "a.com/m", "v1.2.3",
				&internal.Readme{
					Filepath: "PKG_README.md",
					Contents: "pkg readme",
				},
				newPackage("p", "a.com/m/dir/p")),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := testDB.GetDirectory(ctx, tc.dirPath, tc.modulePath, tc.version)
			if tc.wantNotFoundErr {
				if !errors.Is(err, derrors.NotFound) {
					t.Fatalf("want %v; got = \n%+v, %v", derrors.NotFound, got, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			opts := []cmp.Option{
				cmp.AllowUnexported(source.Info{}, safehtml.HTML{}),
				// The packages table only includes partial license information; it omits the Coverage field.
				cmpopts.IgnoreFields(licenses.Metadata{}, "Coverage"),
			}
			// TODO(golang/go#38513): remove once we start displaying
			// READMEs for directories instead of the top-level module.
			tc.want.Readme = &internal.Readme{
				Filepath: sample.ReadmeFilePath,
				Contents: sample.ReadmeContents,
			}
			if diff := cmp.Diff(tc.want, got, opts...); diff != "" {
				t.Errorf("mismatch (-want, +got):\n%s", diff)
			}
		})
	}
}

func findDirectory(m *internal.Module, path string) *internal.Directory {
	for _, d := range m.Directories {
		if d.Path == path {
			return d
		}
	}
	return nil
}

func TestLegacyGetDirectoryFieldSet(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	defer ResetTestDB(testDB, t)

	m := sample.Module("m.c", sample.VersionString, "d/p")
	m.LegacyPackages[0].Imports = nil
	if err := testDB.InsertModule(ctx, m); err != nil {
		t.Fatal(err)
	}

	got, err := testDB.LegacyGetDirectory(ctx, "m.c/d", "m.c", sample.VersionString, internal.MinimalFields)
	if err != nil {
		t.Fatal(err)
	}
	if g, w := got.Packages[0].DocumentationHTML.String(), internal.StringFieldMissing; g != w {
		t.Errorf("DocumentationHTML = %q, want %q", g, w)
	}
}
