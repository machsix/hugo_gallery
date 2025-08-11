---
title: "{{ .FolderName }}"
date: {{ now }}
categories: [{{ range $i, $cat := .Categories }}{{ if $i }}, {{ end }}"{{ $cat }}"{{ end }}]
---

## Images

{{ range .Images }}
<img src="{{ . }}" loading="lazy" /><br>
{{ end }}