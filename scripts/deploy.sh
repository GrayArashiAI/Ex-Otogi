#!/bin/sh
# Deploy the current checkout to a test host, or roll back to the previous
# binary. The host runs the bot as a systemd service from DEPLOY_DIR, which
# holds otogi, otogi.yaml, otogi.env, and data/ (see docs/runbooks).
#
#   scripts/deploy.sh            build, upload, validate config, swap, restart, wait for readiness
#   scripts/deploy.sh rollback   restore the previous binary and restart
#
# Once ready, both list the modules that are on but not running, and why
# (this needs jq here). The deploy still succeeds: such a module does not stop
# the bot, so without the list it would go unnoticed until the panel is opened.
set -eu
host=${DEPLOY_HOST:?set DEPLOY_HOST}
dir=${DEPLOY_DIR:?set DEPLOY_DIR}
service=${DEPLOY_SERVICE:?set DEPLOY_SERVICE}
ready=${DEPLOY_READY_URL:?set DEPLOY_READY_URL}

# wait_ready runs on the host: wait for readiness, or show the logs and fail.
wait_ready="for i in \$(seq 60); do curl -sf $ready >/dev/null && { echo \"$service is ready\"; exit 0; }; sleep 1; done
echo \"$service not ready after 60s:\"; journalctl -u $service -n 40 --no-pager -o cat; exit 1"

# report lists the modules that are on but not running, from the management
# API beside the readiness URL. Failing to read them, it says so, and the
# deploy still succeeds.
report() {
	api=${ready%/readyz}/api/modules
	if ! command -v jq >/dev/null; then
		echo "could not read the modules: jq is not installed here"
	elif ! modules=$(ssh "$host" "curl -fsS $api") ||
		! down=$(printf '%s' "$modules" | jq -r '.[] | select(.enabled and (.running | not)) | "  \(.id): \(.problem // "")"'); then
		echo "could not read the modules from $api"
	elif [ -n "$down" ]; then
		printf 'on but not running:\n%s\n' "$down"
	fi
}

if [ "${1:-}" = rollback ]; then
	# Copy then rename: the running binary cannot be overwritten in place.
	ssh "$host" "set -e; cd $dir; test -f otogi.prev; cp -p otogi.prev otogi.new; mv otogi.new otogi; systemctl restart $service; $wait_ready"
	report
	exit
fi

revision=$(git rev-parse --short HEAD)
if [ -n "$(git status --porcelain)" ]; then
	echo "warning: deploying $revision with uncommitted changes" >&2
fi
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o "$tmp/otogi" ./cmd/otogi
sum=$(sha256sum "$tmp/otogi" | cut -d' ' -f1)
upload=/tmp/otogi.$sum
scp -q "$tmp/otogi" "$host:$upload"
echo "deploying $revision ($sum) to $host:$dir"
ssh "$host" "set -e
echo '$sum  $upload' | sha256sum -c --quiet -
cd $dir
# Validate the deployed config with the new binary before swapping it in.
sudo -u www-data env \$(xargs < otogi.env) $upload -config otogi.yaml validate
cp -p otogi otogi.prev
install -o www-data -g www-data -m 0755 $upload otogi.new && mv otogi.new otogi && rm $upload
systemctl restart $service
$wait_ready"
report
