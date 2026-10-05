package cmdtest

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rudrankriyam/App-Store-Connect-CLI/cmd"
	"github.com/rudrankriyam/App-Store-Connect-CLI/internal/readonly"
)

func TestReadOnlyVersionLocalizationImportPreservesCreateAndUpdateRefusals(t *testing.T) {
	setupAuth(t)
	t.Setenv(readonly.EnvVar, "1")
	file := writeLocalizationImportFile(t, `{"de-DE":{"name":"New"},"en-US":{"name":"Updated"}}`)
	installDefaultTransport(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet || req.URL.Path != "/v1/subscriptionVersions/ver-1/localizations" {
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
			return nil, io.ErrUnexpectedEOF
		}
		return jsonResponse(http.StatusOK, `{"data":[{"type":"subscriptionLocalizations","id":"loc-en","attributes":{"locale":"en-US","name":"Old"}}]}`)
	}))
	var code int
	stdout, stderr := captureOutput(t, func() {
		code = cmd.Run([]string{"subscriptions", "versions", "localizations", "import", "--version-id", "ver-1", "--file", file, "--confirm", "--output", "json"}, "1.0.0")
	})
	if code != cmd.ExitReadOnly {
		t.Fatalf("exit=%d want %d; stderr=%q", code, cmd.ExitReadOnly, stderr)
	}
	receipt := decodeLocalizationImportReceipt(t, stdout)
	if receipt.Total != 2 || receipt.Failed != 2 || len(receipt.Results) != 2 {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
	for i, action := range []string{"create", "update"} {
		item := receipt.Results[i]
		if item.Action != action || item.Status != "failed" || !strings.Contains(item.Error, "ASC_READ_ONLY is set; refusing") {
			t.Fatalf("result[%d]=%+v", i, item)
		}
	}
	if strings.Count(stderr, "Error:") != 1 || !strings.Contains(stderr, "2 of 2 locales failed") {
		t.Fatalf("stderr=%q", stderr)
	}
}

func TestReadOnlyPriceImportAfterEarlierFailureExitsReadOnly(t *testing.T) {
	for _, firstFailure := range []string{"invalid price", "API failure"} {
		t.Run(firstFailure, func(t *testing.T) {
			setupAuth(t)
			t.Setenv(readonly.EnvVar, "1")
			t.Chdir(t.TempDir())
			input := filepath.Join(t.TempDir(), "prices.csv")
			if err := os.WriteFile(input, []byte("territory,price\nUSA,19.99\nJPN,19.99\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			installDefaultTransport(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.Method != http.MethodGet {
					t.Errorf("mutation escaped read-only client: %s %s", req.Method, req.URL.Path)
					return nil, io.ErrUnexpectedEOF
				}
				switch req.URL.Path {
				case "/v1/subscriptions/8000000001/prices":
					return jsonResponse(http.StatusOK, `{"data":[],"links":{}}`)
				case "/v1/subscriptions/8000000001/pricePoints":
					if req.URL.Query().Get("filter[territory]") == "USA" {
						if firstFailure == "API failure" {
							return jsonResponse(http.StatusBadRequest, `{"errors":[{"status":"400","code":"INVALID_REQUEST","title":"Invalid request"}]}`)
						}
						return jsonResponse(http.StatusOK, `{"data":[],"links":{}}`)
					}
					return jsonResponse(http.StatusOK, `{"data":[{"type":"subscriptionPricePoints","id":"pp-jpn","attributes":{"customerPrice":"19.99"}}],"links":{}}`)
				default:
					t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
					return nil, io.ErrUnexpectedEOF
				}
			}))
			var code int
			stdout, stderr := captureOutput(t, func() {
				code = cmd.Run([]string{"subscriptions", "pricing", "prices", "import", "--subscription-id", "8000000001", "--input", input, "--confirm", "--output", "json"}, "1.0.0")
			})
			if code != cmd.ExitReadOnly {
				t.Fatalf("exit=%d want %d; stderr=%q stdout=%q", code, cmd.ExitReadOnly, stderr, stdout)
			}
			var receipt struct {
				Failed   int `json:"failed"`
				Failures []struct {
					Error string `json:"error"`
				} `json:"failures"`
			}
			if err := json.Unmarshal([]byte(stdout), &receipt); err != nil {
				t.Fatal(err)
			}
			if receipt.Failed != 2 || len(receipt.Failures) != 2 || !strings.Contains(receipt.Failures[1].Error, "ASC_READ_ONLY is set; refusing") {
				t.Fatalf("unexpected receipt: %+v", receipt)
			}
			if strings.Contains(receipt.Failures[0].Error, "ASC_READ_ONLY is set; refusing") {
				t.Fatalf("first failure was not distinct: %+v", receipt.Failures[0])
			}
		})
	}
}
