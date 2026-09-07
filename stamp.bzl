"""Link-time stamped variables shared by every tdiscuss binary.

The values are workspace status keys produced by status.sh and require
--stamp (or a --workspace_status_command) to be substituted; without it
rules_go leaves them empty.
"""

STAMP_X_DEFS = {
    "github.com/imeyer/tdiscuss.version": "{STABLE_VERSION}",
    "github.com/imeyer/tdiscuss.gitSha": "{STABLE_GIT_SHA}",
}
