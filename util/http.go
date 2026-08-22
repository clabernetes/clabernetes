package util

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	claberneteserrors "github.com/clabernetes/clabernetes/errors"
)

// IsURL returns true if the given path string starts with http or https.
func IsURL(path string) bool {
	return strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://")
}

// WriteHTTPContentsFromPath writes content at the http path `path` into the writer w.
func WriteHTTPContentsFromPath(
	ctx context.Context,
	path string,
	w io.Writer,
	headerData map[string]string,
) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, path, http.NoBody)
	if err != nil {
		return err
	}

	for k, v := range headerData {
		req.Header.Set(k, v)
	}

	client := http.Client{}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf(
			"%w: non 200 status attempting to load content at '%s', status code: %d",
			claberneteserrors.ErrUtil,
			path,
			resp.StatusCode,
		)
	}

	_, err = io.Copy(w, resp.Body)
	if err != nil {
		return err
	}

	return nil
}

// GitHubNormalToRawLink try to convert a "normal" github link to a raw link.
func GitHubNormalToRawLink(path string) string {
	if !strings.Contains(path, "github.com") {
		return path
	}

	if strings.Contains(path, "githubusercontent") {
		return path
	}

	p := regexp.MustCompile(
		`(?mi)https?:\/\/(?:www\.)?github\.com\/` +
			`(?P<GroupRepo>.*?)(?:\/(?:blob\/)|(?:tree\/))(?P<Path>.*)`,
	)

	paramsMap := RegexStringSubMatchToMap(p, path)

	return fmt.Sprintf(
		"https://raw.githubusercontent.com/%s/%s", paramsMap["GroupRepo"], paramsMap["Path"],
	)
}

// GitHubGroupAndRepoFromURL attempts to return the GitHub group/user and repository from a GitHub
// URL.
func GitHubGroupAndRepoFromURL(path string) (group, repo string) {
	if !strings.Contains(path, "github.com") {
		return group, repo
	}

	p := regexp.MustCompile(
		`(?mi).*.\.com/(?P<Group>.*?)/(?P<Repo>.*)`,
	)

	paramsMap := RegexStringSubMatchToMap(p, path)

	return paramsMap["Group"], strings.Split(paramsMap["Repo"], "/")[0]
}
