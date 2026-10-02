// Package job parses, validates and canonicalizes declarative job
// specifications (spec §12, §7.4.2).
package job

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

const APIVersion = "pumat.org/v1alpha1"

// Spec is the YAML authoring form of a job (§12.2).
type Spec struct {
	APIVersion  string      `yaml:"apiVersion"`
	Kind        string      `yaml:"kind"`
	Metadata    Metadata    `yaml:"metadata"`
	Solver      SolverRef   `yaml:"solver"`
	Resources   Resources   `yaml:"resources"`
	System      System      `yaml:"system"`
	Calculation Calculation `yaml:"calculation"`
	Publication Publication `yaml:"publication"`
	Delivery    Delivery    `yaml:"delivery"`
}

type Metadata struct {
	Name       string            `yaml:"name"`
	Visibility string            `yaml:"visibility"`
	Labels     map[string]string `yaml:"labels"`
}

type SolverRef struct {
	Name           string `yaml:"name"`
	Version        string `yaml:"version"`
	ManifestDigest string `yaml:"manifestDigest"`
	Entrypoint     string `yaml:"entrypoint"`
}

type Resources struct {
	CPU      CPU    `yaml:"cpu"`
	Memory   string `yaml:"memory"`
	Walltime string `yaml:"walltime"`
	GPU      GPU    `yaml:"gpu"`
}

type CPU struct {
	Cores int `yaml:"cores"`
}

type GPU struct {
	Count int `yaml:"count"`
}

type System struct {
	Structure        StructureRef         `yaml:"structure"`
	Pseudopotentials map[string]PseudoRef `yaml:"pseudopotentials"`
}

type StructureRef struct {
	File string `yaml:"file"`
}

type PseudoRef struct {
	Filename string `yaml:"filename"`
	Digest   string `yaml:"digest"`
	Source   string `yaml:"source"`
}

type Calculation struct {
	Type       string         `yaml:"type"`
	Parameters map[string]any `yaml:"parameters"`
}

type Publication struct {
	Input        bool   `yaml:"input"`
	RawOutput    bool   `yaml:"rawOutput"`
	ParsedOutput bool   `yaml:"parsedOutput"`
	Provenance   bool   `yaml:"provenance"`
	License      string `yaml:"license"`
}

type Delivery struct {
	Mode            string  `yaml:"mode"`
	ResultRetention string  `yaml:"resultRetention"`
	Custodian       *string `yaml:"custodian"`
}

// forbiddenFields may never appear anywhere in a public v1 job (§12.3).
var forbiddenFields = map[string]bool{
	"command": true, "shell": true, "script": true, "dockerImage": true,
	"entrypointOverride": true, "hostMount": true, "privileged": true,
	"hostNetwork": true, "capAdd": true, "arbitraryEnvironment": true,
	"postRunCommand": true, "preRunCommand": true, "env": true, "image": true,
}

// ParseYAML strictly parses a job file: no anchors, aliases, merge keys,
// custom tags, duplicate keys, unknown fields, or forbidden fields.
func ParseYAML(data []byte) (*Spec, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("job: %w", err)
	}
	if err := checkNode(&root, ""); err != nil {
		return nil, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var s Spec
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("job: %w", err)
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); err == nil {
		return nil, errors.New("job: multiple YAML documents are not allowed")
	}
	return &s, nil
}

var standardTags = map[string]bool{
	"!!str": true, "!!int": true, "!!float": true, "!!bool": true,
	"!!null": true, "!!map": true, "!!seq": true,
}

func checkNode(n *yaml.Node, path string) error {
	if n.Kind == yaml.AliasNode || n.Anchor != "" {
		return fmt.Errorf("job: YAML anchors/aliases are not allowed (at %s)", orRoot(path))
	}
	if n.Style&yaml.TaggedStyle != 0 || (n.Tag != "" && !standardTags[n.Tag] && n.Kind != yaml.DocumentNode) {
		return fmt.Errorf("job: custom YAML tag %q is not allowed (at %s)", n.Tag, orRoot(path))
	}
	if n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i]
			if k.Value == "<<" {
				return fmt.Errorf("job: YAML merge keys are not allowed (at %s)", orRoot(path))
			}
			if forbiddenFields[k.Value] {
				return fmt.Errorf("job: field %q is forbidden: public jobs cannot carry commands, images, or host access (at %s)", k.Value, orRoot(path))
			}
			if err := checkNode(n.Content[i+1], path+"."+k.Value); err != nil {
				return err
			}
		}
		return nil
	}
	for _, c := range n.Content {
		if err := checkNode(c, path); err != nil {
			return err
		}
	}
	return nil
}

func orRoot(p string) string {
	if p == "" {
		return "document root"
	}
	return strings.TrimPrefix(p, ".")
}
