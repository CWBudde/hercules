#!/usr/bin/env sh
set -eu

version=2.1.1
os=$(uname -s | tr '[:upper:]' '[:lower:]')
machine=$(uname -m)

case "$machine" in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
i386 | i686) arch=386 ;;
*)
	echo "treefmt v${version} has no supported release for architecture: ${machine}" >&2
	exit 1
	;;
esac

case "${os}/${arch}" in
darwin/amd64)
	checksum=739f16af5ba7d36d42095ce19652acbb7444e12fdd5b5d9ea9255130b5cb5462
	;;
darwin/arm64)
	checksum=706e27ad1bcfc66f7b03b0ccd71bc79604dd1544f76aa02e12e691940d530f32
	;;
linux/386)
	checksum=09b25deb726a7eb510a5daa4df4c666176de0c73fd63d0b654f35f695c8200a9
	;;
linux/amd64)
	checksum=7b60f09784ef67b6b9e3d8d84f1a7863f3cc7fe8a523f81e1ff36b7e90796b53
	;;
linux/arm64)
	checksum=00ca5cd0ef019137241b192fc768c9f0aca55fdf8dc01458268a32bde7fed27c
	;;
*)
	echo "treefmt v${version} has no supported release for platform: ${os}/${arch}" >&2
	exit 1
	;;
esac

asset="treefmt_${version}_${os}_${arch}.tar.gz"
url="https://github.com/numtide/treefmt/releases/download/v${version}/${asset}"
install_dir="${GOBIN:-$(go env GOPATH)/bin}"
archive=$(mktemp)
trap 'rm -f "$archive"' EXIT HUP INT TERM

mkdir -p "$install_dir"
curl --proto '=https' --tlsv1.2 --fail --show-error --location "$url" --output "$archive"

if command -v sha256sum >/dev/null 2>&1; then
	printf '%s  %s\n' "$checksum" "$archive" | sha256sum --check -
else
	actual=$(shasum -a 256 "$archive" | awk '{print $1}')
	if [ "$actual" != "$checksum" ]; then
		echo "treefmt checksum mismatch: got ${actual}, want ${checksum}" >&2
		exit 1
	fi
fi

tar -C "$install_dir" -xzf "$archive" treefmt
echo "Installed treefmt v${version} to ${install_dir}/treefmt"
