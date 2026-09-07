#!/bin/sh
set -eu

secret_dir=/run/ares-auth
mkdir -p "$secret_dir"
umask 077

generate_secret() {
	secret_path=$1
	if [ -s "$secret_path" ]; then
		return
	fi
	temporary_path="${secret_path}.tmp"
	head -c 48 /dev/urandom | base64 | tr -d '\n' >"$temporary_path"
	mv "$temporary_path" "$secret_path"
}

generate_secret "$secret_dir/root_key"
generate_secret "$secret_dir/bootstrap_token"
generate_secret "$secret_dir/settings_encryption_key"

# The application image runs as a non-root user. The dedicated volume is only
# mounted by this initializer and Ares, so read-only file permissions keep the
# secrets usable without baking an image-specific numeric UID into Compose.
chmod 0444 "$secret_dir/root_key" "$secret_dir/bootstrap_token" "$secret_dir/settings_encryption_key"

if [ "${ARES_AUTH_SECRETS_PRINT_BOOTSTRAP:-false}" = "true" ]; then
	printf '%s\n' "$(cat "$secret_dir/bootstrap_token")"
else
	printf '%s\n' 'Ares authentication secrets are initialized.'
fi
