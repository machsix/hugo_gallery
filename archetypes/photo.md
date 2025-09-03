---
title: "{{ if gt (len .Videos) 0 }}🎥 {{ end }}{{ .FolderName }}"
date: {{ .Date }}
tags: [{{ range $i, $cat := .Tags }}{{ if $i }}, {{ end }}"{{ $cat }}"{{ end }}]
type: "post"      # or omit; default is usually "post" or "page"
---


{{ range .Videos }}
{{ printf "<video src=\"/images/%s/%s\" controls title=\"%s\" preload=\"metadata\" style=\"max-width:100%%\"></video>" $.FolderSHA (urlquery .) (html .)}}

{{ end }}


{{ range .Images }}
{{ printf "{{< responsive-img src=\"/images/%s/%s\" alt=\"%s\" >}}" $.FolderSHA (urlquery .) (html .) }}
{{ end }}


