// Copyright The Helm Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package releaser

import (
	"bufio"
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/helm/chart-releaser/pkg/github"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"helm.sh/helm/v3/pkg/provenance"
	"helm.sh/helm/v3/pkg/repo"

	"github.com/helm/chart-releaser/pkg/config"
)

type FakeGitHub struct {
	mock.Mock
	release    *github.Release
	isTagExist bool
}

type FakeGit struct {
	mock.Mock
}

type MockClient struct {
	statusCode int
	file       string
}

func (m *MockClient) Get(url string) (*http.Response, error) {
	if m.statusCode == http.StatusOK {
		file, _ := os.Open(m.file)
		reader := bufio.NewReader(file)
		return &http.Response{StatusCode: http.StatusOK, Body: ioutil.NopCloser(reader)}, nil
	} else {
		return &http.Response{StatusCode: http.StatusNotFound, Body: ioutil.NopCloser(nil)}, nil
	}
}

func (f *FakeGitHub) CreateRelease(ctx context.Context, input *github.Release) error {
	f.Called(ctx, input)
	f.release = input
	return nil
}

func (f *FakeGitHub) GetRelease(ctx context.Context, tag string) (*github.Release, error) {
	release := &github.Release{
		Name:        "testdata/release-packages/test-chart-0.1.0",
		Description: "A Helm chart for Kubernetes",
		Assets: []*github.Asset{
			{
				Path: "testdata/release-packages/test-chart-0.1.0.tgz",
				URL:  "https://myrepo/charts/test-chart-0.1.0.tgz",
			},
		},
	}
	return release, nil
}

func (f *FakeGitHub) DeleteRelease(ctx context.Context, tag string) error {
	f.Called(ctx, tag)
	// we delete tags
	f.isTagExist = false
	return nil
}

func (f *FakeGitHub) IsTagExist(ctx context.Context, tag string) (bool, error) {
	f.Called(ctx, tag)
	return f.isTagExist, nil
}

func (f *FakeGitHub) CreatePullRequest(owner string, repo string, message string, head string, base string) (string, error) {
	f.Called(owner, repo, message, head, base)
	return "https://github.com/owner/repo/pull/42", nil
}

func (g *FakeGit) AddWorktree(workingDir string, committish string) (string, error) {
	return workingDir, nil
}

func (g *FakeGit) RemoveWorktree(workingDir string, path string) error {
	return nil
}

func (g *FakeGit) Add(workingDir string, args ...string) error {
	return nil
}

func (g *FakeGit) Commit(workingDir string, message string) error {
	return nil
}

func (g *FakeGit) Push(workingDir string, args ...string) error {
	g.Called(workingDir, args)
	return nil
}

func TestReleaser_UpdateIndexFile(t *testing.T) {
	indexDir, _ := ioutil.TempDir(".", "index")
	defer os.RemoveAll(indexDir)

	fakeGitHub := new(FakeGitHub)

	tests := []struct {
		name     string
		exists   bool
		releaser *Releaser
	}{
		{
			"index-file-exists",
			true,
			&Releaser{
				config: &config.Options{
					IndexPath:   "testdata/index/index.yaml",
					PackagePath: "testdata/release-packages",
				},
				github:     fakeGitHub,
				httpClient: &MockClient{http.StatusOK, "testdata/repo/index.yaml"},
			},
		},
		{
			"index-file-does-not-exist",
			false,
			&Releaser{
				config: &config.Options{
					IndexPath:   filepath.Join(indexDir, "index.yaml"),
					PackagePath: "testdata/release-packages",
				},
				github:     fakeGitHub,
				httpClient: &MockClient{http.StatusNotFound, ""},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var sha256 string
			if tt.exists {
				sha256, _ = provenance.DigestFile(tt.releaser.config.IndexPath)
			}
			update, err := tt.releaser.UpdateIndexFile()
			assert.NoError(t, err)
			assert.Equal(t, update, !tt.exists)
			if tt.exists {
				newSha256, _ := provenance.DigestFile(tt.releaser.config.IndexPath)
				assert.Equal(t, sha256, newSha256)
			} else {
				_, err := os.Stat(tt.releaser.config.IndexPath)
				assert.NoError(t, err)
			}
		})
	}
}

func TestReleaser_UpdateIndexFileGenerated(t *testing.T) {
	indexDir, _ := ioutil.TempDir(".", "index")
	defer os.RemoveAll(indexDir)

	fakeGitHub := new(FakeGitHub)

	tests := []struct {
		name     string
		releaser *Releaser
	}{
		{
			"index-file-exists",
			&Releaser{
				config: &config.Options{
					IndexPath:   filepath.Join(indexDir, "index.yaml"),
					PackagePath: "testdata/release-packages",
				},
				github:     fakeGitHub,
				httpClient: &MockClient{http.StatusOK, "testdata/empty-repo/index.yaml"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			indexFile, _ := repo.LoadIndexFile("testdata/empty-repo/index.yaml")
			generated := indexFile.Generated
			update, err := tt.releaser.UpdateIndexFile()
			assert.NoError(t, err)
			assert.True(t, update)
			newIndexFile, _ := repo.LoadIndexFile(tt.releaser.config.IndexPath)
			newGenerated := newIndexFile.Generated
			assert.True(t, newGenerated.After(generated))
		})
	}
}

func TestReleaser_splitPackageNameAndVersion(t *testing.T) {
	tests := []struct {
		name     string
		pkg      string
		expected []string
	}{
		{
			"no-hyphen",
			"foo",
			nil,
		},
		{
			"one-hyphen",
			"foo-1.2.3",
			[]string{"foo", "1.2.3"},
		},
		{
			"two-hyphens",
			"foo-bar-1.2.3",
			[]string{"foo-bar", "1.2.3"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &Releaser{}
			if tt.expected == nil {
				assert.Panics(t, func() {
					r.splitPackageNameAndVersion(tt.pkg)
				}, "slice bounds out of range")
			} else {
				actual := r.splitPackageNameAndVersion(tt.pkg)
				assert.Equal(t, tt.expected, actual)
			}
		})
	}
}

func TestReleaser_addToIndexFile(t *testing.T) {
	tests := []struct {
		name    string
		chart   string
		version string
		error   bool
	}{
		{
			"invalid-package",
			"does-not-exist",
			"0.1.0",
			true,
		},
		{
			"valid-package",
			"test-chart",
			"0.1.0",
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &Releaser{
				config: &config.Options{PackagePath: "testdata/release-packages"},
			}
			indexFile := repo.NewIndexFile()
			url := fmt.Sprintf("https://myrepo/charts/%s-%s.tgz", tt.chart, tt.version)
			err := r.addToIndexFile(indexFile, url)
			if tt.error {
				assert.Error(t, err)
				assert.False(t, indexFile.Has(tt.chart, tt.version))
			} else {
				assert.True(t, indexFile.Has(tt.chart, tt.version))
			}
		})
	}
}

func TestReleaser_CreateReleases(t *testing.T) {
	tests := []struct {
		name        string
		packagePath string
		chart       string
		version     string
		commit      string
		force       bool
		error       bool
	}{
		{
			"invalid-package-path",
			"testdata/does-not-exist",
			"test-chart",
			"0.1.0",
			"",
			false,
			true,
		},
		{
			"valid-package-path",
			"testdata/release-packages",
			"test-chart",
			"0.1.0",
			"",
			false,
			false,
		},
		{
			"valid-package-path-with-commit",
			"testdata/release-packages",
			"test-chart",
			"0.1.0",
			"5e239bd19fbefb9eb0181ecf0c7ef73b8fe2753c",
			false,
			false,
		},
		{
			"valid-package-path-with-force-recreate",
			"testdata/release-packages",
			"test-chart",
			"0.1.0",
			"",
			true,
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeGitHub := new(FakeGitHub)
			fakeGit := new(FakeGit)
			r := &Releaser{
				config: &config.Options{
					PackagePath:         tt.packagePath,
					Commit:              tt.commit,
					ReleaseNameTemplate: "{{ .Name }}-{{ .Version }}",
					Force:               tt.force,
				},
				github: fakeGitHub,
				git:    fakeGit,
			}
			fakeGitHub.On("CreateRelease", mock.Anything, mock.Anything).Return(nil)
			fakeGitHub.On("DeleteRelease", mock.Anything, mock.Anything).Return(nil)
			fakeGitHub.On("IsTagExist", mock.Anything, mock.Anything).Return(nil)
			fakeGit.On("Push", mock.Anything, mock.Anything, mock.Anything).Return(nil)
			err := r.CreateReleases()
			if tt.error {
				assert.Error(t, err)
				assert.Nil(t, fakeGitHub.release)
				fakeGitHub.AssertNumberOfCalls(t, "CreateRelease", 0)
			} else {
				assert.NoError(t, err)
				releaseName := fmt.Sprintf("%s-%s", tt.chart, tt.version)
				assetPath := fmt.Sprintf("%s/%s-%s.tgz", r.config.PackagePath, tt.chart, tt.version)
				releaseDescription := "A Helm chart for Kubernetes"
				assert.Equal(t, releaseName, fakeGitHub.release.Name)
				assert.Equal(t, releaseDescription, fakeGitHub.release.Description)
				assert.Len(t, fakeGitHub.release.Assets, 1)
				assert.Equal(t, assetPath, fakeGitHub.release.Assets[0].Path)
				assert.Equal(t, tt.commit, fakeGitHub.release.Commit)
				fakeGitHub.AssertNumberOfCalls(t, "CreateRelease", 1)
			}
			if tt.force {
				fakeGitHub.AssertNumberOfCalls(t, "DeleteRelease", 1)
				fakeGitHub.AssertNumberOfCalls(t, "IsTagExist", 1)
				fakeGit.AssertNumberOfCalls(t, "Push", 1)
			} else {
				fakeGitHub.AssertNumberOfCalls(t, "DeleteRelease", 0)
				fakeGitHub.AssertNumberOfCalls(t, "IsTagExist", 0)
				fakeGit.AssertNumberOfCalls(t, "Push", 0)
			}
		})
	}
}
