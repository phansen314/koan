#!/usr/bin/env bash
# Downloads the fzf releases the end-to-end picker tests run against
# (pick-spec.md, Testing): the minimum, 0.63.0, and the current release.
# Each tarball is checked against the sha256 pinned below before it is
# unpacked, to DIR/fzf-VERSION/fzf. Prints the binaries' paths joined by
# ':', ready for KOAN_E2E_FZF:
#
#   export KOAN_E2E_FZF=$(scripts/fetch-fzf.sh ~/.cache/koan-fzf)
#   go test ./e2e -run TestPick
#
# Already-fetched versions are reused. Linux and macOS, amd64 and arm64.
set -euo pipefail

dir=${1:?usage: fetch-fzf.sh DIR}

versions=(0.63.0 0.74.4)

# sha256 of fzf-VERSION-PLATFORM.tar.gz, from each release's checksums file.
sum() {
	case $1-$2 in
	0.63.0-linux_amd64) echo 36e60fe51fed2f72954b39b9ce1e7ea72c1dc79bc99f4a3c2a7b98bb1a2b49bb ;;
	0.63.0-linux_arm64) echo bb5dafdd566e2bcea4fe7d8ba8d97cad15d733c6d05fc04e7b6da8126a0a2f82 ;;
	0.63.0-darwin_amd64) echo ddd99faf0aefff30efa5d972358156f4f1e80ae8a7d6d756bfdc7b633a781fc6 ;;
	0.63.0-darwin_arm64) echo 2f3a1ac15ee28df1e23112bcc3eacf2d2488d1d2d0477159a4c3b21338b4ea91 ;;
	0.74.4-linux_amd64) echo 05e6813a337cc722c3ed07e54a764b75cc5d671e2e60459db0ba696ee5fa7504 ;;
	0.74.4-linux_arm64) echo 5d673b849f494f0d64ec471d8640b153ca8849e3846a31da17abdcfce8df6b46 ;;
	0.74.4-darwin_amd64) echo 2d392b50be66e2ab104ccd52a6072df692b1f9b9c5b449a9c098de885f32c4c5 ;;
	0.74.4-darwin_arm64) echo 4f6a113bfc0c7959e0005c78d566a51afc4fcefc956f43735c62a9deb19e92ae ;;
	*) echo "fetch-fzf.sh: no checksum for fzf $1 on $2" >&2; return 1 ;;
	esac
}

case $(uname -s) in
Linux) os=linux ;;
Darwin) os=darwin ;;
*) echo "fetch-fzf.sh: unsupported OS $(uname -s)" >&2; exit 1 ;;
esac
case $(uname -m) in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
*) echo "fetch-fzf.sh: unsupported architecture $(uname -m)" >&2; exit 1 ;;
esac
platform=${os}_$arch

sha256() {
	if command -v sha256sum >/dev/null; then sha256sum "$1"; else shasum -a 256 "$1"; fi | cut -d' ' -f1
}

mkdir -p "$dir"
dir=$(cd "$dir" && pwd)
paths=()
for v in "${versions[@]}"; do
	bin=$dir/fzf-$v/fzf
	if [[ ! -x $bin ]]; then
		want=$(sum "$v" "$platform")
		tmp=$(mktemp -d)
		trap 'rm -rf "$tmp"' EXIT
		tarball=fzf-$v-$platform.tar.gz
		curl -fsSL -o "$tmp/$tarball" "https://github.com/junegunn/fzf/releases/download/v$v/$tarball"
		got=$(sha256 "$tmp/$tarball")
		if [[ $got != "$want" ]]; then
			echo "fetch-fzf.sh: $tarball: sha256 $got, want $want" >&2
			exit 1
		fi
		tar -xzf "$tmp/$tarball" -C "$tmp" fzf
		mkdir -p "$dir/fzf-$v"
		mv "$tmp/fzf" "$bin"
		rm -rf "$tmp"
		trap - EXIT
	fi
	paths+=("$bin")
done
(IFS=:; echo "${paths[*]}")
