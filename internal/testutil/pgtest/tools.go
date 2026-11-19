package pgtest

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/google/uuid"
)

// ToolRunner returns a function that runs pg_dump and pg_restore inside the shared test
// container instead of on the machine running the tests (V4-43). The container ships the client
// tools of exactly the server version production uses, so a backup and restore test does not
// depend on which Postgres client, if any, is installed on a developer machine or a CI runner.
//
// The function has the shape of db.CommandRunner (pgtest does not import db, so it is spelled
// out). It accepts the invocations MyPA makes: `pg_dump -d <url> ...` writing the dump to stdout,
// and `pg_restore ... -d <url>` reading it from stdin. The database named in the URL must live in
// the shared container (it does when it came from NewDatabase).
func ToolRunner() func(ctx context.Context, name string, args, env []string, stdin io.Reader, stdout io.Writer) error {
	return func(ctx context.Context, name string, args, _ []string, stdin io.Reader, stdout io.Writer) error {
		if _, err := AdminDSN(ctx); err != nil {
			return err
		}
		inside, err := insideArgs(args)
		if err != nil {
			return err
		}
		file := "/tmp/pgtest-" + uuid.NewString() + ".dump"
		defer func() { _, _, _ = pgContainer.Exec(context.WithoutCancel(ctx), []string{"rm", "-f", file}) }()

		switch name {
		case "pg_dump":
			if err := execInside(ctx, append(append([]string{"pg_dump"}, inside...), "-f", file)); err != nil {
				return err
			}
			dump, err := pgContainer.CopyFileFromContainer(ctx, file)
			if err != nil {
				return fmt.Errorf("copy the dump out of the container: %w", err)
			}
			defer dump.Close()
			_, err = io.Copy(stdout, dump)
			return err
		case "pg_restore":
			content, err := io.ReadAll(stdin)
			if err != nil {
				return fmt.Errorf("read the dump to restore: %w", err)
			}
			if err := pgContainer.CopyToContainer(ctx, content, file, 0o644); err != nil {
				return fmt.Errorf("copy the dump into the container: %w", err)
			}
			return execInside(ctx, append(append([]string{"pg_restore"}, inside...), file))
		default:
			return fmt.Errorf("pgtest.ToolRunner does not run %q", name)
		}
	}
}

// insideArgs rewrites `-d <url>` for use inside the container: the database is reached over the
// local socket by name, as the role named in the URL (local connections need no password).
// `pg_restore -f -` (convert the archive to SQL without a database) is rewritten to write to
// /dev/null, since the caller only wants to know whether the archive reads to its end.
func insideArgs(args []string) ([]string, error) {
	var out []string
	target := false
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "-d" && i+1 < len(args):
			u, err := url.Parse(args[i+1])
			if err != nil || strings.Trim(u.Path, "/") == "" {
				return nil, fmt.Errorf("pgtest.ToolRunner: -d must be a database URL")
			}
			role := "user"
			if u.User != nil && u.User.Username() != "" {
				role = u.User.Username()
			}
			out = append(out, "-U", role, "-d", strings.Trim(u.Path, "/"))
			target = true
			i++
		case args[i] == "-f" && i+1 < len(args) && args[i+1] == "-":
			out = append(out, "-f", "/dev/null")
			target = true
			i++
		default:
			out = append(out, args[i])
		}
	}
	if !target {
		return nil, fmt.Errorf("pgtest.ToolRunner: expected -d <database URL> or -f -")
	}
	return out, nil
}

func execInside(ctx context.Context, cmd []string) error {
	code, output, err := pgContainer.Exec(ctx, cmd)
	if err != nil {
		return fmt.Errorf("run %s in the container: %w", cmd[0], err)
	}
	if code != 0 {
		detail, _ := io.ReadAll(io.LimitReader(output, 8<<10))
		return fmt.Errorf("%s failed (exit %d): %s", cmd[0], code, strings.TrimSpace(string(detail)))
	}
	return nil
}
