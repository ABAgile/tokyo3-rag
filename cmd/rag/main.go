// rag is the CLI client for the RAG knowledge service.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var (
	serverURL string
	apiToken  string
)

func main() {
	root := &cobra.Command{
		Use:   "rag",
		Short: "RAG knowledge service CLI",
	}

	root.PersistentFlags().StringVar(&serverURL, "server", envOr("RAG_SERVER_URL", "http://localhost:8080"), "RAG server URL")
	root.PersistentFlags().StringVar(&apiToken, "token", os.Getenv("RAG_API_TOKEN"), "Bearer token")

	root.AddCommand(queryCmd(), ingestCmd(), jobsCmd())

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func queryCmd() *cobra.Command {
	var queryType string
	cmd := &cobra.Command{
		Use:   "query <question>",
		Short: "Ask a question about company knowledge",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			q := strings.Join(args, " ")
			return doQuery(q, queryType)
		},
	}
	cmd.Flags().StringVar(&queryType, "type", "both", "Query type: doc, code, or both")
	return cmd
}

func doQuery(query, queryType string) error {
	body, _ := json.Marshal(map[string]string{
		"query":      query,
		"query_type": queryType,
	})
	req, err := newRequest(http.MethodPost, "/api/v1/query", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server %d: %s", resp.StatusCode, b)
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		var event map[string]any
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}
		switch event["type"] {
		case "token":
			fmt.Print(event["content"])
		case "sources":
			fmt.Printf("\n\n--- Sources ---\n")
			if sources, ok := event["sources"].([]any); ok {
				for _, s := range sources {
					if m, ok := s.(map[string]any); ok {
						path, _ := m["path"].(string)
						if ls, ok := m["line_start"].(float64); ok && ls > 0 {
							fmt.Printf("  %s:%d-%d\n", path, int(ls), int(m["line_end"].(float64)))
						} else {
							fmt.Printf("  %s (chunk %v)\n", path, m["chunk_index"])
						}
					}
				}
			}
		case "done":
			fmt.Println()
			return nil
		}
	}
	return scanner.Err()
}

func ingestCmd() *cobra.Command {
	var srcType, language string
	cmd := &cobra.Command{
		Use:   "ingest <path>",
		Short: "Ingest a file or directory",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return doIngest(args[0], srcType, language)
		},
	}
	cmd.Flags().StringVar(&srcType, "type", "code", "Source type: code, pdf, or markdown")
	cmd.Flags().StringVar(&language, "language", "", "Language: go, ruby, clojure (code only)")
	return cmd
}

func doIngest(path, srcType, language string) error {
	body, _ := json.Marshal(map[string]string{
		"path":        path,
		"source_type": srcType,
		"language":    language,
	})
	req, err := newRequest(http.MethodPost, "/api/v1/ingest", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	fmt.Println(string(b))
	return nil
}

func jobsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "jobs",
		Short: "Manage ingest jobs",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List all ingest jobs",
		RunE: func(cmd *cobra.Command, args []string) error {
			req, err := newRequest(http.MethodGet, "/api/v1/ingest/jobs", nil)
			if err != nil {
				return err
			}
			resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			var result map[string]any
			_ = json.NewDecoder(resp.Body).Decode(&result)
			b, _ := json.MarshalIndent(result, "", "  ")
			fmt.Println(string(b))
			return nil
		},
	})
	return cmd
}

func newRequest(method, path string, body io.Reader) (*http.Request, error) {
	url := strings.TrimRight(serverURL, "/") + path
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}
	if apiToken != "" {
		req.Header.Set("Authorization", "Bearer "+apiToken)
	}
	return req, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
