#!/bin/sh
# Build distributable executables; never packages state, credentials or host paths.
set -eu
cd "$(dirname "$0")/.."
release_version=${1:?Usage: sh scripts/release.sh <version>}
case "$release_version" in *[!A-Za-z0-9.+-]*|'') echo 'Invalid version' >&2; exit 1;; esac
pnpm --dir ui install --frozen-lockfile
pnpm --dir ui run build
node scripts/licenses.mjs
release_dir="dist/releases/$release_version"
mkdir -p "$release_dir"
for platform in linux/amd64 linux/arm64 windows/amd64 windows/arm64 darwin/amd64 darwin/arm64; do
 release_os=${platform%/*}
 release_arch=${platform#*/}
 package="TwitchDropsNAS_${release_version}_${release_os}_${release_arch}"
 stage="$release_dir/$package"
 mkdir -p "$stage"
 executable=twitch-drops
 if [ "$release_os" = windows ]; then executable=twitch-drops.exe; fi
 CGO_ENABLED=0 GOOS="$release_os" GOARCH="$release_arch" go build -buildvcs=false -trimpath -ldflags="-s -w -X main.version=$release_version" -o "$stage/$executable" .
 chmod 0755 "$stage/$executable"
 cp LICENSE NOTICE.md THIRD_PARTY_NOTICES.md README.md README.en.md "$stage/"
 cp -R docs "$stage/docs"
 # Archives contain only the executable and these public docs.
 if [ "$release_os" = windows ]; then
  rm -f "$release_dir/$package.zip"
  (cd "$release_dir" && zip -q -r "$package.zip" "$package")
 else
  tar -C "$release_dir" -czf "$release_dir/$package.tar.gz" "$package"
 fi
 rm -r "$stage"
done
(cd "$release_dir" && sha256sum ./*.tar.gz ./*.zip > SHA256SUMS)
echo "Built six platform archives and SHA256SUMS in $release_dir"
