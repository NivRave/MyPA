package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/nivik/mypa/internal/broker"
	"github.com/nivik/mypa/internal/config"
	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/llm"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/transfer"
	"google.golang.org/genai"
)

// `mypa embeddings status|reembed`: the pinned-embeddings tool (V4-03, docs/EMBEDDINGS.md). A
// one-shot process run with the app stopped, like `transfer` and `access`.
const embeddingsUsage = `Usage: mypa embeddings <command>

  status               Show the model the stored memory vectors were made with, the configured
                       model (EMBEDDING_MODEL), the size the schema stores and how many memories
                       have no embedding. Changes nothing.
  reembed              Embed every memory again with EMBEDDING_MODEL and record that model, in
                       one transaction: on any failure nothing is changed.
                         --missing           only the memories without an embedding, in the
                                             recorded model
                         --skip-stop-check   do not require the app to be stopped

Run it where the app runs, with the app stopped, for example:

  docker compose run --rm app embeddings reembed

See docs/EMBEDDINGS.md.
`

// embeddingStore is what the command needs from the database; db.Client implements it.
type embeddingStore interface {
	EmbeddingStatus(ctx context.Context) (db.EmbeddingStatus, error)
	Reembed(ctx context.Context, embed db.EmbedBatchFunc, opts db.ReembedOptions) (db.ReembedResult, error)
}

// embeddingsDeps is everything the command talks to. Production builds it from the environment.
type embeddingsDeps struct {
	Store embeddingStore
	// Model is EMBEDDING_MODEL.
	Model string
	// NewEmbedder builds the embedder for a vector size; called only by reembed.
	NewEmbedder func(ctx context.Context, dims int) (db.EmbedBatchFunc, error)
	// Probe looks at the inbound queue, to tell whether the app is running.
	Probe transfer.QueueProbe
	Out   io.Writer
}

func embeddingsMain(args []string) int {
	out := os.Stdout
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(out, embeddingsUsage)
		return 2
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(out, "✗ cannot load the configuration: %v\n", err)
		return 1
	}
	client, err := db.Open(cfg.Database.URL)
	if err != nil {
		fmt.Fprintf(out, "✗ cannot connect to the database: %v\n", err)
		return 1
	}
	defer func() { _ = client.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()
	sqlDB, err := client.DB.DB()
	if err != nil {
		fmt.Fprintf(out, "✗ %v\n", err)
		return 1
	}
	if err := db.EnsureMigrated(ctx, sqlDB); err != nil {
		fmt.Fprintf(out, "✗ %v\n", err)
		return 1
	}
	deps := embeddingsDeps{
		Store: client,
		Model: cfg.Embedding.Model,
		NewEmbedder: func(ctx context.Context, dims int) (db.EmbedBatchFunc, error) {
			e, err := llm.NewEmbedder(ctx, cfg.Gemini.APIKey, cfg.Embedding.Model, dims)
			if err != nil {
				return nil, err
			}
			return e.EmbedBatch, nil
		},
		Probe: queueProbe(cfg.RabbitMQ.URL),
		Out:   out,
	}
	return runEmbeddings(ctx, args, deps)
}

// runEmbeddings executes one command and returns the process exit code.
func runEmbeddings(ctx context.Context, args []string, deps embeddingsDeps) int {
	out := deps.Out
	fail := func(err error) int {
		fmt.Fprintf(out, "✗ %v\n", err)
		return 1
	}
	if len(args) == 0 {
		fmt.Fprint(out, embeddingsUsage)
		return 2
	}
	verb, rest := args[0], args[1:]
	flags := flag.NewFlagSet("embeddings "+verb, flag.ContinueOnError)
	flags.SetOutput(out)

	switch verb {
	case "status":
		if err := flags.Parse(rest); err != nil || flags.NArg() > 0 {
			return 2
		}
		st, err := deps.Store.EmbeddingStatus(ctx)
		if err != nil {
			return fail(err)
		}
		printEmbeddingStatus(out, st, deps.Model)
		return 0

	case "reembed":
		missing := flags.Bool("missing", false, "")
		skipStop := flags.Bool("skip-stop-check", false, "")
		if err := flags.Parse(rest); err != nil || flags.NArg() > 0 {
			return 2
		}
		if !*skipStop {
			if err := appStopped(ctx, deps.Probe); err != nil {
				return fail(err)
			}
		}
		st, err := deps.Store.EmbeddingStatus(ctx)
		if err != nil {
			return fail(err)
		}
		embed, err := deps.NewEmbedder(ctx, st.ColumnDimensions)
		if err != nil {
			return fail(err)
		}
		what := "every memory"
		if *missing {
			what = "the memories without an embedding"
		}
		fmt.Fprintf(out, "Embedding %s with %s (%d dimensions)…\n", what, deps.Model, st.ColumnDimensions)
		lastReport := time.Now()
		res, err := deps.Store.Reembed(ctx, embed, db.ReembedOptions{
			Model:       deps.Model,
			MissingOnly: *missing,
			BatchSize:   llm.EmbedBatchSize,
			Progress: func(done, total int) {
				if done == total || time.Since(lastReport) > 10*time.Second {
					fmt.Fprintf(out, "  %d of %d\n", done, total)
					lastReport = time.Now()
				}
			},
		})
		if err != nil {
			return fail(fmt.Errorf("%w\nNothing was changed", err))
		}
		fmt.Fprintf(out, "✓ embedded %d memories with %s (%d dimensions)", res.Embedded, res.Model, res.Dimensions)
		if res.Empty > 0 {
			fmt.Fprintf(out, "; %d with no text were left without an embedding", res.Empty)
		}
		fmt.Fprintln(out, ".")
		if res.Previous != "" && res.Previous != res.Model {
			fmt.Fprintf(out, "  The recorded model changed from %s to %s. Start the app with EMBEDDING_MODEL=%s.\n", res.Previous, res.Model, res.Model)
		}
		return 0
	}
	fmt.Fprintf(out, "✗ unknown command %q\n\n%s", verb, embeddingsUsage)
	return 2
}

func printEmbeddingStatus(out io.Writer, st db.EmbeddingStatus, configured string) {
	recorded := "none recorded"
	if st.Recorded != nil {
		recorded = fmt.Sprintf("%s (%d dimensions, since %s)", st.Recorded.Model, st.Recorded.Dimensions, st.Recorded.UpdatedAt.Format("2006-01-02 15:04 MST"))
	}
	fmt.Fprintf(out, "Stored vectors:     %s\n", recorded)
	fmt.Fprintf(out, "EMBEDDING_MODEL:    %s\n", configured)
	fmt.Fprintf(out, "Schema stores:      %d dimensions\n", st.ColumnDimensions)
	fmt.Fprintf(out, "Memories:           %d, of which %d have no embedding\n", st.Memories, st.WithoutEmbedding)
	switch {
	case st.Recorded == nil || st.Recorded.Model != configured || st.Recorded.Dimensions != st.ColumnDimensions:
		fmt.Fprintln(out, "! The configured model is not the one the stored vectors were made with: run `mypa embeddings reembed` before starting the app.")
	case st.WithoutEmbedding > 0:
		fmt.Fprintln(out, "! Some memories are not found by meaning: `mypa embeddings reembed --missing` embeds them.")
	default:
		fmt.Fprintln(out, "✓ Every memory is embedded with the configured model.")
	}
}

// appStopped refuses while the worker consumes the inbound queue: the re-embed holds a write lock
// on the memories, and a running app would wait on it for every memory it saves.
func appStopped(ctx context.Context, probe transfer.QueueProbe) error {
	if probe == nil {
		return errors.New("cannot tell whether the app is running; pass --skip-stop-check if it is stopped")
	}
	_, consumers, err := probe(ctx, broker.InboundMessagesQueue)
	if err != nil {
		return fmt.Errorf("cannot tell whether the app is running (%v); pass --skip-stop-check if it is stopped", err)
	}
	if consumers > 0 {
		return fmt.Errorf("the app is running (%s has %d consumer(s)): stop it first (docker compose stop app)", broker.InboundMessagesQueue, consumers)
	}
	return nil
}

// prepareEmbeddings is the worker's start-up step for embeddings (V4-03). It builds the embedder
// from EMBEDDING_MODEL, never from the chat model, and refuses to start when the stored vectors
// were made by another model or have another size than this build stores. It never changes a
// stored vector.
func prepareEmbeddings(ctx context.Context, cfg *config.Config, client *db.Client, opts ...func(*genai.ClientConfig)) (*llm.Embedder, error) {
	st, err := client.EmbeddingStatus(ctx)
	if err != nil {
		return nil, err
	}
	if st.ColumnDimensions != models.EmbeddingDimensions {
		return nil, fmt.Errorf("the schema stores vectors of %d numbers and this build %d: run the migrations of this release (docs/EMBEDDINGS.md)",
			st.ColumnDimensions, models.EmbeddingDimensions)
	}
	if err := client.CheckEmbeddingSpace(ctx, cfg.Embedding.Model); err != nil {
		return nil, err
	}
	if st.WithoutEmbedding > 0 {
		slog.Warn("some memories have no embedding and are not recalled by meaning; `mypa embeddings reembed --missing` embeds them",
			"memories", st.WithoutEmbedding)
	}
	return llm.NewEmbedder(ctx, cfg.Gemini.APIKey, cfg.Embedding.Model, models.EmbeddingDimensions, opts...)
}
