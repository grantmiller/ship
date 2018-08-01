package specs

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"strings"
	"testing"

	"github.com/go-kit/kit/log"

	"github.com/replicatedhq/ship/pkg/constants"

	"github.com/google/go-github/github"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/spf13/afero"
)

var client *github.Client
var mux *http.ServeMux
var serverURL string
var teardown func()

func setupGitClient() (client *github.Client, mux *http.ServeMux, serveURL string, teardown func()) {
	mux = http.NewServeMux()
	server := httptest.NewServer(mux)
	client = github.NewClient(nil)
	url, _ := url.Parse(server.URL + "/")
	client.BaseURL = url
	client.UploadURL = url

	return client, mux, server.URL, server.Close
}

func TestGithubClient(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "GithubClient")
}

var _ = Describe("GithubClient", func() {
	client, mux, serverURL, teardown = setupGitClient()
	mux.HandleFunc("/repos/o/r/tarball", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, serverURL+"/archive.tar.gz", http.StatusFound)
		return
	})
	mux.HandleFunc("/archive.tar.gz", func(w http.ResponseWriter, r *http.Request) {
		archiveData := `H4sIAJKjXFsAA+3WXW6CQBQFYJbCBmrv/D831ce+uIOpDtGEKQaoibt3qERbEmiNI6TxfC8TIwkXTg65lfW73D3ZcrXZ7t1zcg9EZJRKv059OonL09lKmRDcMM6k0SkxSYolqbrLNB2fVW3LMIoPr2DounBZlg383z7H+fwnqp/5v25sWc8O1ucR7xHeh5ZyKH9xzl+TDPkroylJKeIMvR48//fw8PC4Ov1fLl7mb4uZX8e8xzX9V4Y1/RdMof9jyIpi6hFgQp3+1y78tLWrYm6CV+1/oum/JqGx/42hN/+12+XFwbuPsA7euA3++v1n/LL/sZA/JyM4vv9juMQ89SQwhd7+V67cb1fu5vInf9n/zLf+y6b/nDP0fwxtzFOPAQAAAAAAAAAAAACRHQEZehxJACgAAA==`
		dec := base64.NewDecoder(base64.StdEncoding, strings.NewReader(archiveData))
		w.Header().Set("Content-Type", "application/gzip")
		io.Copy(w, dec)
	})

	Describe("GetChartAndReadmeContents", func() {
		Context("With a url prefixed with http(s)", func() {
			It("should fetch and persist README.md and Chart.yaml", func() {
				validGitURLWithPrefix := "http://www.github.com/o/r/"
				mockFs := afero.Afero{Fs: afero.NewMemMapFs()}
				gitClient := GithubClient{
					client: client,
					fs:     mockFs,
					logger: log.NewNopLogger(),
				}

				gitClient.GetChartAndReadmeContents(context.Background(), validGitURLWithPrefix)

				readme, err := gitClient.fs.ReadFile(path.Join(constants.KustomizeHelmPath, "README.md"))
				Expect(err).NotTo(HaveOccurred())
				chart, err := gitClient.fs.ReadFile(path.Join(constants.KustomizeHelmPath, "Chart.yaml"))
				Expect(err).NotTo(HaveOccurred())
				deployment, err := gitClient.fs.ReadFile(path.Join(constants.KustomizeHelmPath, "templates", "deployment.yml"))
				Expect(err).NotTo(HaveOccurred())
				service, err := gitClient.fs.ReadFile(path.Join(constants.KustomizeHelmPath, "templates", "service.yml"))
				Expect(err).NotTo(HaveOccurred())

				Expect(string(readme)).To(Equal("foo"))
				Expect(string(chart)).To(Equal("bar"))
				Expect(string(deployment)).To(Equal("deployment"))
				Expect(string(service)).To(Equal("service"))
			})
		})

		Context("With a url not prefixed with http", func() {
			It("should fetch and persist README.md and Chart.yaml", func() {
				validGitURLWithoutPrefix := "github.com/o/r"
				mockFs := afero.Afero{Fs: afero.NewMemMapFs()}
				gitClient := GithubClient{
					client: client,
					fs:     mockFs,
					logger: log.NewNopLogger(),
				}

				gitClient.GetChartAndReadmeContents(context.Background(), validGitURLWithoutPrefix)
				readme, err := gitClient.fs.ReadFile(path.Join(constants.KustomizeHelmPath, "README.md"))
				Expect(err).NotTo(HaveOccurred())
				chart, err := gitClient.fs.ReadFile(path.Join(constants.KustomizeHelmPath, "Chart.yaml"))
				Expect(err).NotTo(HaveOccurred())
				deployment, err := gitClient.fs.ReadFile(path.Join(constants.KustomizeHelmPath, "templates", "deployment.yml"))
				Expect(err).NotTo(HaveOccurred())
				service, err := gitClient.fs.ReadFile(path.Join(constants.KustomizeHelmPath, "templates", "service.yml"))
				Expect(err).NotTo(HaveOccurred())

				Expect(string(readme)).To(Equal("foo"))
				Expect(string(chart)).To(Equal("bar"))
				Expect(string(deployment)).To(Equal("deployment"))
				Expect(string(service)).To(Equal("service"))
			})
		})
	})

	Describe("decodeGitHubUrl", func() {
		Context("With a valid github url", func() {
			It("should decode a valid url without a path", func() {
				chartPath := "github.com/o/r"
				o, r, p, err := decodeGitHubUrl(chartPath)
				Expect(err).NotTo(HaveOccurred())

				Expect(o).To(Equal("o"))
				Expect(r).To(Equal("r"))
				Expect(p).To(Equal(""))
			})

			It("should decode a valid url with a path", func() {
				chartPath := "github.com/o/r/stable/chart"
				o, r, p, err := decodeGitHubUrl(chartPath)
				Expect(err).NotTo(HaveOccurred())

				Expect(o).To(Equal("o"))
				Expect(r).To(Equal("r"))
				Expect(p).To(Equal("stable/chart"))
			})
		})

		Context("With an invalid github url", func() {
			It("should failed to decode a url without a path", func() {
				chartPath := "github.com"
				_, _, _, err := decodeGitHubUrl(chartPath)
				Expect(err).NotTo(BeNil())
				Expect(err.Error()).To(Equal("github.com: unable to decode github url"))
			})

			It("should failed to decode a url with a path", func() {
				chartPath := "github.com/o"
				_, _, _, err := decodeGitHubUrl(chartPath)
				Expect(err).NotTo(BeNil())
				Expect(err.Error()).To(Equal("github.com/o: unable to decode github url"))
			})
		})
	})
})

var _ = AfterSuite(func() {
	teardown()
})