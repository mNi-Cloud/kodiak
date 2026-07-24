#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmp_dir="$(mktemp -d)"
trap 'rm -rf "${tmp_dir}"' EXIT

for resource in authkeys connectors tailnets; do
  source_file="${repo_root}/config/crd/bases/kodiak.mnicloud.jp_${resource}.yaml"
  chart_file="${repo_root}/dist/chart/templates/crd/kodiak.mnicloud.jp_${resource}.yaml"
  rendered_body="${tmp_dir}/${resource}.yaml"

  awk '
    /\{\{- if \.Values\.crd\.(enable|keep) \}\}/ { next }
    /helm\.sh\/resource-policy: keep/ { next }
    /\{\{- end \}\}/ { next }
    { print }
  ' "${chart_file}" >"${rendered_body}"

  diff -u "${source_file}" "${rendered_body}"
done

awk '/^rules:/ { copy = 1 } copy { print }' \
  "${repo_root}/config/rbac/role.yaml" >"${tmp_dir}/generated-rules.yaml"
awk '
  /^rules:/ { copy = 1 }
  copy && !/\{\{- end \}\}/ { print }
' "${repo_root}/dist/chart/templates/rbac/role.yaml" >"${tmp_dir}/chart-rules.yaml"
diff -u "${tmp_dir}/generated-rules.yaml" "${tmp_dir}/chart-rules.yaml"
