package main

import (
	"bytes"
	"embed"
	"fmt"
	"strings"
	"sync"
	"text/template"
)

//go:embed cloudinit/*
var cloudInitFS embed.FS

type cloudInitTemplateData struct {
	Timezone         string
	Locale           string
	PrimaryUser      string
	BaseInitScript   string
	BlockInitScript  string
	BlockInitService string
	BlockInitTimer   string
}

var (
	cloudConfigOnce sync.Once
	cloudConfigTmpl *template.Template
	cloudConfigErr  error
)

func indent(spaces int, s string) string {
	if spaces <= 0 || s == "" {
		return s
	}
	pad := strings.Repeat(" ", spaces)

	var b strings.Builder
	b.Grow(len(s) + spaces*8)

	atLineStart := true
	for i := 0; i < len(s); i++ {
		if atLineStart {
			b.WriteString(pad)
			atLineStart = false
		}
		ch := s[i]
		b.WriteByte(ch)
		if ch == '\n' {
			atLineStart = true
		}
	}

	return b.String()
}

func cloudConfigTemplate() (*template.Template, error) {
	cloudConfigOnce.Do(func() {
		raw, err := cloudInitFS.ReadFile("cloudinit/cloud-config.yaml.tmpl")
		if err != nil {
			cloudConfigErr = fmt.Errorf("read cloud-config template: %w", err)
			return
		}

		cloudConfigTmpl, cloudConfigErr = template.New("cloud-config.yaml.tmpl").
			Funcs(template.FuncMap{
				"indent": indent,
			}).
			Parse(string(raw))
		if cloudConfigErr != nil {
			cloudConfigErr = fmt.Errorf("parse cloud-config template: %w", cloudConfigErr)
		}
	})

	return cloudConfigTmpl, cloudConfigErr
}

func renderCloudConfig(primaryUser string) (string, error) {
	baseScript, err := cloudInitFS.ReadFile("cloudinit/paropal-base-init.sh")
	if err != nil {
		return "", fmt.Errorf("read base-init script: %w", err)
	}
	blockScript, err := cloudInitFS.ReadFile("cloudinit/paropal-block-init.sh")
	if err != nil {
		return "", fmt.Errorf("read block-init script: %w", err)
	}
	blockService, err := cloudInitFS.ReadFile("cloudinit/paropal-block-init.service")
	if err != nil {
		return "", fmt.Errorf("read block-init service: %w", err)
	}
	blockTimer, err := cloudInitFS.ReadFile("cloudinit/paropal-block-init.timer")
	if err != nil {
		return "", fmt.Errorf("read block-init timer: %w", err)
	}

	tmpl, err := cloudConfigTemplate()
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	err = tmpl.Execute(&buf, cloudInitTemplateData{
		Timezone:         cloudInitTimeZone,
		Locale:           cloudInitLocale,
		PrimaryUser:      primaryUser,
		BaseInitScript:   string(baseScript),
		BlockInitScript:  string(blockScript),
		BlockInitService: string(blockService),
		BlockInitTimer:   string(blockTimer),
	})
	if err != nil {
		return "", fmt.Errorf("render cloud-config: %w", err)
	}

	return buf.String(), nil
}
