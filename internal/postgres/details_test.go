// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/google/safehtml"
	"golang.org/x/pkgsite/internal"
	"golang.org/x/pkgsite/internal/derrors"
	"golang.org/x/pkgsite/internal/experiment"
	"golang.org/x/pkgsite/internal/licenses"
	"golang.org/x/pkgsite/internal/source"
	"golang.org/x/pkgsite/internal/testing/sample"
)

func TestPostgres_GetModuleInfo(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	defer ResetTestDB(testDB, t)

	testCases := []struct {
		name, path, version string
		modules             []*internal.Module
		wantIndex           int // index into versions
		wantErr             error
	}{
		{
			name:    "version present",
			path:    "mod.1",
			version: "v1.0.2",
			modules: []*internal.Module{
				sample.Module("mod.1", "v1.1.0", sample.Suffix),
				sample.Module("mod.1", "v1.0.2", sample.Suffix),
				sample.Module("mod.1", "v1.0.0", sample.Suffix),
			},
			wantIndex: 1,
		},
		{
			name:    "version not present",
			path:    "mod.2",
			version: "v1.0.3",
			modules: []*internal.Module{
				sample.Module("mod.2", "v1.1.0", sample.Suffix),
				sample.Module("mod.2", "v1.0.2", sample.Suffix),
				sample.Module("mod.2", "v1.0.0", sample.Suffix),
			},
			wantErr: derrors.NotFound,
		},
		{
			name:    "no versions",
			path:    "mod3",
			version: "v1.2.3",
			wantErr: derrors.NotFound,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			for _, v := range tc.modules {
				if err := testDB.InsertModule(ctx, v); err != nil {
					t.Error(err)
				}
			}

			gotVI, err := testDB.GetModuleInfo(ctx, tc.path, tc.version)
			if err != nil {
				if tc.wantErr == nil {
					t.Fatalf("got unexpected error %v", err)
				}
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("got error %v, want Is(%v)", err, tc.wantErr)
				}
				return
			}
			if tc.wantIndex >= len(tc.modules) {
				t.Fatal("wantIndex too large")
			}
			wantVI := &tc.modules[tc.wantIndex].ModuleInfo
			if diff := cmp.Diff(wantVI, gotVI, cmpopts.EquateEmpty(), cmp.AllowUnexported(source.Info{})); diff != "" {
				t.Errorf("mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestPostgres_GetVersionInfo_Latest(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	defer ResetTestDB(testDB, t)

	testCases := []struct {
		name, path string
		modules    []*internal.Module
		wantIndex  int // index into versions
		wantErr    error
	}{
		{
			name: "largest release",
			path: "mod.1",
			modules: []*internal.Module{
				sample.Module("mod.1", "v1.1.0-alpha.1", sample.Suffix),
				sample.Module("mod.1", "v1.0.0", sample.Suffix),
				sample.Module("mod.1", "v1.0.0-20190311183353-d8887717615a", sample.Suffix),
			},
			wantIndex: 1,
		},
		{
			name: "largest prerelease",
			path: "mod.2",
			modules: []*internal.Module{
				sample.Module("mod.2", "v1.1.0-beta.10", sample.Suffix),
				sample.Module("mod.2", "v1.1.0-beta.2", sample.Suffix),
				sample.Module("mod.2", "v1.0.0-20190311183353-d8887717615a", sample.Suffix),
			},
			wantIndex: 0,
		},
		{
			name:    "no versions",
			path:    "mod3",
			wantErr: derrors.NotFound,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			for _, v := range tc.modules {
				if err := testDB.InsertModule(ctx, v); err != nil {
					t.Error(err)
				}
			}

			gotVI, err := testDB.LegacyGetModuleInfo(ctx, tc.path, internal.LatestVersion)
			if err != nil {
				if tc.wantErr == nil {
					t.Fatalf("got unexpected error %v", err)
				}
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("got error %v, want Is(%v)", err, tc.wantErr)
				}
				return
			}
			if tc.wantIndex >= len(tc.modules) {
				t.Fatal("wantIndex too large")
			}
			wantVI := &tc.modules[tc.wantIndex].LegacyModuleInfo
			if diff := cmp.Diff(wantVI, gotVI, cmpopts.EquateEmpty(), cmp.AllowUnexported(source.Info{})); diff != "" {
				t.Errorf("mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestPostgres_GetImportsAndImportedBy(t *testing.T) {
	var (
		m1          = sample.Module("path.to/foo", "v1.1.0", "bar")
		m2          = sample.Module("path2.to/foo", "v1.2.0", "bar2")
		m3          = sample.Module("path3.to/foo", "v1.3.0", "bar3")
		testModules = []*internal.Module{m1, m2, m3}

		pkg1 = m1.LegacyPackages[0]
		pkg2 = m2.LegacyPackages[0]
		pkg3 = m3.LegacyPackages[0]
	)

	pkg1.Imports = nil
	pkg2.Imports = []string{pkg1.Path}
	pkg3.Imports = []string{pkg2.Path, pkg1.Path}
	m1.Directories[1].Package.Imports = pkg1.Imports
	m2.Directories[1].Package.Imports = pkg2.Imports
	m3.Directories[1].Package.Imports = pkg3.Imports

	for _, tc := range []struct {
		name, path, modulePath, version string
		wantImports                     []string
		wantImportedBy                  []string
	}{
		{
			name:           "multiple imports no imported by",
			path:           pkg3.Path,
			modulePath:     m3.ModulePath,
			version:        "v1.3.0",
			wantImports:    pkg3.Imports,
			wantImportedBy: nil,
		},
		{
			name:           "one import one imported by",
			path:           pkg2.Path,
			modulePath:     m2.ModulePath,
			version:        "v1.2.0",
			wantImports:    pkg2.Imports,
			wantImportedBy: []string{pkg3.Path},
		},
		{
			name:           "no imports two imported by",
			path:           pkg1.Path,
			modulePath:     m1.ModulePath,
			version:        "v1.1.0",
			wantImports:    nil,
			wantImportedBy: []string{pkg2.Path, pkg3.Path},
		},
		{
			name:           "no imports one imported by",
			path:           pkg1.Path,
			modulePath:     m2.ModulePath, // should cause pkg2 to be excluded.
			version:        "v1.1.0",
			wantImports:    nil,
			wantImportedBy: []string{pkg3.Path},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer ResetTestDB(testDB, t)

			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			for _, v := range testModules {
				if err := testDB.InsertModule(ctx, v); err != nil {
					t.Error(err)
				}
			}
			t.Run("use-package-imports", func(t *testing.T) {
				testGetImports(ctx, t, tc.path, tc.modulePath, tc.version, tc.wantImports, internal.ExperimentUsePackageImports)
			})
			t.Run("use-imports", func(t *testing.T) {
				testGetImports(ctx, t, tc.path, tc.modulePath, tc.version, tc.wantImports)
			})

			gotImportedBy, err := testDB.GetImportedBy(ctx, tc.path, tc.modulePath, 100)
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tc.wantImportedBy, gotImportedBy); diff != "" {
				t.Errorf("testDB.GetImportedBy(%q, %q) mismatch (-want +got):\n%s", tc.path, tc.modulePath, diff)
			}
		})
	}
}

func testGetImports(ctx context.Context, t *testing.T, path, modulePath, version string, wantImports []string, experimentNames ...string) {
	t.Helper()
	ctx = experiment.NewContext(ctx, experimentNames...)
	got, err := testDB.GetImports(ctx, path, modulePath, version)
	if err != nil {
		t.Fatal(err)
	}

	sort.Strings(wantImports)
	if diff := cmp.Diff(wantImports, got); diff != "" {
		t.Errorf("testDB.GetImports(%q, %q) mismatch (-want +got):\n%s", path, version, diff)
	}
}

func TestGetPackagesInVersion(t *testing.T) {
	testVersion := sample.Module("test.module", "v1.2.3", "", "foo")

	for _, tc := range []struct {
		name, pkgPath string
		module        *internal.Module
	}{
		{
			name:    "version with multiple packages",
			pkgPath: "test.module",
			module:  testVersion,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer ResetTestDB(testDB, t)
			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			if err := testDB.InsertModule(ctx, tc.module); err != nil {
				t.Error(err)
			}

			got, err := testDB.LegacyGetPackagesInModule(ctx, tc.pkgPath, tc.module.Version)
			if err != nil {
				t.Fatal(err)
			}

			opts := []cmp.Option{
				cmpopts.IgnoreFields(internal.LegacyPackage{}, "Imports"),
				// The packages table only includes partial license information; it omits the Coverage field.
				cmpopts.IgnoreFields(licenses.Metadata{}, "Coverage"),
				cmp.AllowUnexported(safehtml.HTML{}),
			}
			if diff := cmp.Diff(tc.module.LegacyPackages, got, opts...); diff != "" {
				t.Errorf("testDB.GetPackageInVersion(ctx, %q, %q) mismatch (-want +got):\n%s", tc.pkgPath, tc.module.Version, diff)
			}
		})
	}
}

func TestJSONBScanner(t *testing.T) {
	type S struct{ A int }

	want := &S{1}
	val, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}

	var got *S
	js := jsonbScanner{&got}
	if err := js.Scan(val); err != nil {
		t.Fatal(err)
	}
	if *got != *want {
		t.Errorf("got %+v, want %+v", *got, *want)
	}

	var got2 *S
	js = jsonbScanner{&got2}
	if err := js.Scan(nil); err != nil {
		t.Fatal(err)
	}
	if got2 != nil {
		t.Errorf("got %#v, want nil", got2)
	}
}

func TestHackUpDocumentation(t *testing.T) {
	tests := []struct {
		body string
		want string
	}{
		{"nothing burger", "nothing burger"},
		{`<a href="/pkg/foo">foo</a>`, `<a href="/foo?tab=doc">foo</a>`},
		{`<a href="/pkg/foo"`, `<a href="/pkg/foo"`},
		{
			`<a href="/pkg/foo"><a href="/pkg/bar">bar</a></a>`,
			`<a href="/foo?tab=doc"><a href="/pkg/bar">bar</a></a>`,
		},
		{
			`<a href="/pkg/foo">foo</a>
		   <a href="/pkg/bar">bar</a>`,
			`<a href="/foo?tab=doc">foo</a>
		   <a href="/bar?tab=doc">bar</a>`,
		},
		{`<ahref="/pkg/foo">foo</a>`, `<ahref="/pkg/foo">foo</a>`},
		{`<allhref="/pkg/foo">foo</a>`, `<allhref="/pkg/foo">foo</a>`},
		{`<a nothref="/pkg/foo">foo</a>`, `<a nothref="/pkg/foo">foo</a>`},
		{`<a href="/pkg/foo#identifier">foo</a>`, `<a href="/foo?tab=doc#identifier">foo</a>`},
		{`<a href="#identifier">foo</a>`, `<a href="#identifier">foo</a>`},
		{`<span id="Indirect.Type"></span>func (in <a href="#Indirect">Indirect</a>) Type() <a href="/pkg/reflect">reflect</a>.<a href="/pkg/reflect#Type">Type</a>`,
			`<span id="Indirect.Type"></span>func (in <a href="#Indirect">Indirect</a>) Type() <a href="/reflect?tab=doc">reflect</a>.<a href="/reflect?tab=doc#Type">Type</a>`},
	}

	for _, test := range tests {
		if got := hackUpDocumentation(test.body); got != test.want {
			t.Errorf("hackUpDocumentation(%s) = %s, want %s", test.body, got, test.want)
		}
	}
}
