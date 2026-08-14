package actions

import (
	"context"
	"regexp"
	"strings"

	"bodsch.me/mailcow-dockerapi/internal/dockerclient"
)

// rspamdPasswordFile is where the controller expects its password.
//
// G101: a path into the rspamd configuration, not a credential.
const rspamdPasswordFile = "/etc/rspamd/override.d/worker-controller-password.inc" //nolint:gosec

var (
	// rspamdHashPattern cuts the hash out of a line, starting at the $2$ prefix.
	rspamdHashPattern = regexp.MustCompile(`\$2\$.+$`)
	// rspamdSanitize removes everything that is not part of the hash.
	rspamdSanitize = regexp.MustCompile(`[^0-9a-zA-Z$]+`)
)

// RspamdWorkerPassword implements container_post__exec__rspamd__worker_password.
//
// The sequence follows the original: rspamadm generates the hash, it is written to
// the override file, read back for verification, and the container is restarted
// afterwards.
//
// Both commands run through an interactive shell with stdin attached — a regular
// docker exec produces no result for rspamadm pw.
func RspamdWorkerPassword(ctx context.Context, env Env, req Request, t dockerclient.Target) Result {
	raw, ok := req.String("raw")
	if !ok {
		return Danger("raw is missing")
	}

	c, errRes := firstContainer(ctx, env, t, execListAll)
	if errRes != nil {
		return *errRes
	}

	genCmd := "/usr/bin/rspamadm pw -e -p " + shellQuote(raw) + " 2> /dev/null"

	out, err := env.Docker.ExecInteractive(ctx, c.ID, dockerclient.InteractiveOptions{
		Command: genCmd,
		User:    "_rspamd",
	})
	if err != nil {
		env.logger().Error("failed changing Rspamd password", "err", err)
		return Danger("command did not complete")
	}

	if !applyRspamdHash(ctx, env, c.ID, out) {
		env.logger().Error("failed changing Rspamd password")
		return Danger("command did not complete")
	}

	env.logger().Info("success changing Rspamd password")
	return Success()
}

// applyRspamdHash looks for the generated hash in the output, writes it to the
// override file and restarts the container. The return value corresponds to the
// original's matched flag.
func applyRspamdHash(ctx context.Context, env Env, containerID, output string) bool {
	matched := false

	for _, line := range strings.Split(output, "\n") {
		if !strings.Contains(line, "$2$") {
			continue
		}

		hash := rspamdHashPattern.FindString(strings.TrimSpace(line))
		if hash == "" {
			// $2$ sat at the end of the line with no hash after it. In Python the
			// access to group(0) failed here.
			continue
		}

		hash = rspamdSanitize.ReplaceAllString(hash, "")
		if !strings.HasPrefix(hash, "$2$") {
			continue
		}

		writeCmd := "/bin/echo 'enable_password = \"" + hash + "\";' > " +
			rspamdPasswordFile + " && cat " + rspamdPasswordFile

		verify, err := env.Docker.ExecInteractive(ctx, containerID, dockerclient.InteractiveOptions{
			Command: writeCmd,
			User:    "_rspamd",
		})
		if err != nil {
			continue
		}

		// Only once the file read back contains the hash does the change count as
		// applied.
		if !strings.Contains(verify, hash) {
			continue
		}

		if err := env.Docker.Restart(ctx, containerID); err != nil {
			continue
		}

		matched = true
	}

	return matched
}
