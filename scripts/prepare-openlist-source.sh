#!/bin/sh

set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
project_dir=$(CDPATH= cd -- "${script_dir}/.." && pwd)
openlist_dir=${1:-}
openlist_ref=${2:-HEAD}
frontend_release=${3:-auto}
prepared_root="${project_dir}/.docker"
prepared_dir="${prepared_root}/openlist-src"

if [ -z "${openlist_dir}" ]; then
    echo "Usage: $0 /path/to/OpenList [git-ref] [frontend-release|local]" >&2
    exit 2
fi

if ! git -C "${openlist_dir}" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    echo "Not an OpenList git checkout: ${openlist_dir}" >&2
    exit 2
fi

case "${prepared_dir}" in
    "${project_dir}/.docker/openlist-src") ;;
    *)
        echo "Refusing unexpected output path: ${prepared_dir}" >&2
        exit 2
        ;;
esac

mkdir -p "${prepared_root}"
temporary_dir=$(mktemp -d "${prepared_root}/openlist-src.tmp.XXXXXX")
trap 'rm -rf "${temporary_dir}"' EXIT HUP INT TERM

git -C "${openlist_dir}" archive "${openlist_ref}" | tar -x -C "${temporary_dir}"

if [ "${frontend_release}" = "auto" ]; then
    exact_tag=$(git -C "${openlist_dir}" describe --exact-match --tags "${openlist_ref}" 2>/dev/null || true)
    case "${exact_tag}" in
        v[0-9]*) frontend_release=${exact_tag} ;;
        *) frontend_release=edge ;;
    esac
fi

rm -rf "${temporary_dir}/public/dist"
mkdir -p "${temporary_dir}/public/dist"

if [ "${frontend_release}" = "local" ]; then
    if [ ! -f "${openlist_dir}/public/dist/index.html" ]; then
        echo "Missing ${openlist_dir}/public/dist/index.html" >&2
        echo "Build or download the OpenList frontend before using 'local'." >&2
        exit 2
    fi
    cp -a "${openlist_dir}/public/dist/." "${temporary_dir}/public/dist/"
    frontend_source=local
else
    "${script_dir}/download-openlist-frontend.sh" \
        "${frontend_release}" "${temporary_dir}/public/dist"
    frontend_source=${frontend_release}
fi

if [ ! -f "${temporary_dir}/public/dist/index.html" ]; then
    echo "Frontend package does not contain index.html." >&2
    exit 2
fi

# The setup API was added after v4.2.6. Refuse the exact mismatch that causes
# a fresh instance to show the login page instead of the initialization wizard.
if grep -q '"/init_status"' "${temporary_dir}/server/router.go"; then
    if ! grep -R -a -q -E '/api/public/init_status|/init_status' "${temporary_dir}/public/dist"; then
        echo "The backend has the initialization API, but frontend '${frontend_source}' does not use it." >&2
        echo "Use the matching edge frontend instead of the v4.2.6 frontend." >&2
        exit 2
    fi
fi

source_version=$(git -C "${openlist_dir}" describe --tags --always "${openlist_ref}")
printf '%s\n' "${source_version}" >"${temporary_dir}/.openlist-source-version"
printf '%s\n' "${frontend_source}" >"${temporary_dir}/.openlist-frontend-source"

rm -rf "${prepared_dir}"
mv "${temporary_dir}" "${prepared_dir}"
trap - EXIT HUP INT TERM

web_version=$(cat "${prepared_dir}/public/dist/VERSION" 2>/dev/null || printf unknown)
echo "Prepared OpenList backend ${source_version} with frontend ${web_version} (${frontend_source}) at ${prepared_dir}"
