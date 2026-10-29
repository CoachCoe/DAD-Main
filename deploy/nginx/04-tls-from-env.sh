#!/bin/sh
# Launched by the image entrypoint before 05-ingress.envsh and the template rendering.
#
# ECS can inject a secret only as an environment variable, never as a file, but nginx reads
# its certificate and key from /etc/nginx/tls (nginx.conf). When INGRESS_TLS_FULLCHAIN and
# INGRESS_TLS_PRIVKEY are set, write them there. When neither is set (Compose bind-mounts
# the directory) this does nothing.
#
# This writes files only. It cannot remove the variables from nginx's environment: the
# entrypoint runs `*.sh` hooks in a child shell and `*.envsh` hooks in a pipeline
# subshell, so nothing a hook unsets reaches the `exec nginx` that follows. That is
# harmless for nginx, which never reads environment variables at run time, but it means
# the key stays readable to anything that can read the container's environment.
set -eu
tls_dir=/etc/nginx/tls
fullchain="${INGRESS_TLS_FULLCHAIN:-}"
privkey="${INGRESS_TLS_PRIVKEY:-}"
[ -n "$fullchain$privkey" ] || exit 0
if [ -z "$fullchain" ] || [ -z "$privkey" ]; then
    echo "ingress: set both INGRESS_TLS_FULLCHAIN and INGRESS_TLS_PRIVKEY, or neither" >&2
    exit 1
fi
umask 077
printf '%s\n' "$fullchain" > "$tls_dir/fullchain.pem"
printf '%s\n' "$privkey" > "$tls_dir/privkey.pem"
