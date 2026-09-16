#!/bin/sh

set -eu

frontend_release=${1:-}
destination_dir=${2:-}

if [ -z "${frontend_release}" ] || [ -z "${destination_dir}" ]; then
    echo "Usage: $0 FRONTEND_RELEASE DESTINATION_DIR" >&2
    exit 2
fi

for command_name in curl jq sha256sum tar; do
    if ! command -v "${command_name}" >/dev/null 2>&1; then
        echo "Required command not found: ${command_name}" >&2
        exit 2
    fi
done

destination_parent=$(dirname -- "${destination_dir}")
mkdir -p "${destination_parent}"
temporary_dir=$(mktemp -d "${destination_parent}/openlist-frontend.tmp.XXXXXX")
trap 'rm -rf "${temporary_dir}"' EXIT HUP INT TERM

release_api="https://api.github.com/repos/OpenListTeam/OpenList-Frontend/releases/tags/${frontend_release}"
release_json="${temporary_dir}/release.json"
frontend_archive="${temporary_dir}/frontend.tar.gz"
extracted_dir="${temporary_dir}/dist"

fetch_url() {
    output_file=$1
    url=$2
    if [ -n "${OPENLIST_GITHUB_TOKEN:-}" ]; then
        curl -fsSL --retry 5 --retry-all-errors --connect-timeout 20 \
            -H "Accept: application/vnd.github+json" \
            -H "Authorization: Bearer ${OPENLIST_GITHUB_TOKEN}" \
            -o "${output_file}" "${url}"
    else
        curl -fsSL --retry 5 --retry-all-errors --connect-timeout 20 \
            -H "Accept: application/vnd.github+json" \
            -o "${output_file}" "${url}"
    fi
}

echo "Resolving OpenList frontend release: ${frontend_release}"
fetch_url "${release_json}" "${release_api}"

asset_filter='.assets[]? | select(.name | test("^openlist-frontend-dist-.*\\.tar\\.gz$")) | select(.name | contains("-lite") | not)'
asset_count=$(jq "[${asset_filter}] | length" "${release_json}")
if [ "${asset_count}" -ne 1 ]; then
    echo "Expected one standard frontend archive in release '${frontend_release}', found ${asset_count}." >&2
    exit 2
fi

asset_url=$(jq -r "${asset_filter} | .browser_download_url" "${release_json}")
asset_digest=$(jq -r "${asset_filter} | .digest // empty" "${release_json}")

echo "Downloading ${asset_url}"
fetch_url "${frontend_archive}" "${asset_url}"

case "${asset_digest}" in
    sha256:*)
        expected_sha256=${asset_digest#sha256:}
        actual_sha256=$(sha256sum "${frontend_archive}" | awk '{print $1}')
        if [ "${actual_sha256}" != "${expected_sha256}" ]; then
            echo "Frontend archive SHA-256 mismatch." >&2
            exit 2
        fi
        ;;
    *)
        echo "Frontend release does not provide a SHA-256 digest." >&2
        exit 2
        ;;
esac

mkdir -p "${extracted_dir}"
tar -xzf "${frontend_archive}" -C "${extracted_dir}"
if [ ! -f "${extracted_dir}/index.html" ]; then
    echo "Frontend package does not contain index.html." >&2
    exit 2
fi

rm -rf "${destination_dir}"
mv "${extracted_dir}" "${destination_dir}"
trap - EXIT HUP INT TERM
rm -rf "${temporary_dir}"

web_version=$(cat "${destination_dir}/VERSION" 2>/dev/null || printf unknown)
echo "Prepared OpenList frontend ${web_version} (${frontend_release}) at ${destination_dir}"
