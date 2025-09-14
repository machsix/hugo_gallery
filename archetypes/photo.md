---
title: "{{ if gt (len .Videos) 0 }}🎥 {{ end }}{{ .FolderName }}"
date: {{ .Date }}
tags: [{{ range $i, $cat := .Tags }}{{ if $i }}, {{ end }}"{{ $cat }}"{{ end }}]
type: "post"      # or omit; default is usually "post" or "page"
---

{{ range $index, $video := .Videos }}
  {{ $src := printf "/images/%s/%s" $.FolderSHA (urlquery $video) }}
  {{ $id := printf "video-%d" $index }}
  {{ printf "{{< artvideo id=\"%s\" url=\"%s\" title=\"%s\" style=\"max-width:100%%\">}}" $id $src (html $video) }}
{{ end }}


{{ range .Images }}
{{ printf "{{< responsive-img src=\"/images/%s/%s\" alt=\"%s\" >}}" $.FolderSHA (urlquery .) (html .) }}
{{ end }}


