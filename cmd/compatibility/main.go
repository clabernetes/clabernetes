//nolint:funlen,gocognit,gocyclo,noinlineerr,wsl_v5 // Keep command dispatch compact.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	clabernetesinternalcompatibility "github.com/clabernetes/clabernetes/internal/compatibility"
)

const commandTimeout = 2 * time.Minute

var (
	errExtractSourceRequired = errors.New("extract-registry requires -source or -module-version")
	errUnknownMode           = errors.New("unknown compatibility mode")
)

func main() {
	mode := flag.String(
		"mode",
		"verify",
		"operation: verify, extract-registry, render-doc, or refresh-invalidation",
	)
	baselinePath := flag.String(
		"baseline",
		clabernetesinternalcompatibility.DefaultBaselinePath,
		"compatibility baseline path",
	)
	sourceDir := flag.String(
		"source",
		"",
		"containerlab source directory; defaults to the pinned Go module",
	)
	moduleVersion := flag.String(
		"module-version",
		"",
		"containerlab module version for extraction (for example v0.78.0)",
	)
	flag.Parse()

	switch *mode {
	case "extract-registry":
		source := *sourceDir
		if source == "" {
			if *moduleVersion == "" {
				fatal(errExtractSourceRequired)
			}
			download := downloadContainerlab(*moduleVersion)
			source = download.Dir
		}
		registrations, extractErr := clabernetesinternalcompatibility.ExtractRegistry(source)
		if extractErr != nil {
			fatal(extractErr)
		}
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if encodeErr := encoder.Encode(registrations); encodeErr != nil {
			fatal(fmt.Errorf("encoding registry: %w", encodeErr))
		}
	case "render-doc":
		baseline, err := clabernetesinternalcompatibility.LoadBaseline(*baselinePath)
		if err != nil {
			fatal(err)
		}
		documentation := clabernetesinternalcompatibility.RenderDocumentation(baseline)
		if _, err = os.Stdout.Write(documentation); err != nil {
			fatal(fmt.Errorf("writing compatibility documentation: %w", err))
		}
	case "refresh-invalidation":
		baseline, err := clabernetesinternalcompatibility.LoadBaseline(*baselinePath)
		if err != nil {
			fatal(err)
		}
		current, err := clabernetesinternalcompatibility.ComputeInvalidation(".")
		if err != nil {
			fatal(err)
		}
		baseline.Invalidation = current
		if err = clabernetesinternalcompatibility.SaveBaseline(*baselinePath,
			baseline); err != nil {
			fatal(err)
		}
		if _, err = fmt.Fprintln(
			os.Stdout,
			"refreshed compatibility invalidation digests",
		); err != nil {
			fatal(fmt.Errorf("writing refresh result: %w", err))
		}
	case "verify":
		baseline, err := clabernetesinternalcompatibility.LoadBaseline(*baselinePath)
		if err != nil {
			fatal(err)
		}

		var download *clabernetesinternalcompatibility.ModuleDownload
		if *sourceDir == "" {
			download = downloadContainerlab(baseline.Containerlab.ModuleVersion)
		} else {
			download = &clabernetesinternalcompatibility.ModuleDownload{
				Path:    baseline.Containerlab.Module,
				Version: baseline.Containerlab.ModuleVersion,
				Sum:     baseline.Containerlab.ModuleSum,
				Dir:     *sourceDir,
				Origin: clabernetesinternalcompatibility.ModuleOrigin{
					Hash: baseline.Containerlab.Commit,
				},
			}
		}
		if err = baseline.VerifyUpstream(download); err != nil {
			fatal(err)
		}
		if err = baseline.VerifyRepository("."); err != nil {
			fatal(err)
		}
		if _, err = fmt.Fprintf(
			os.Stdout,
			"verified containerlab %s compatibility baseline and live registry\n",
			baseline.Containerlab.Version,
		); err != nil {
			fatal(fmt.Errorf("writing verification result: %w", err))
		}
	default:
		fatal(fmt.Errorf("%w %q", errUnknownMode, *mode))
	}
}

func downloadContainerlab(version string) *clabernetesinternalcompatibility.ModuleDownload {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()

	download, err := clabernetesinternalcompatibility.DownloadModule(
		ctx,
		"github.com/srl-labs/containerlab",
		version,
	)
	if err != nil {
		fatal(err)
	}

	return download
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "compatibility: %v\n", err)
	os.Exit(1)
}
