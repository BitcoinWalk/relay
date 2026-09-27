package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"fiatjaf.com/nostr"
)

type cityDirectoryAnchorFile struct {
	Version int                   `json:"version"`
	Cities  []cityDirectoryAnchor `json:"cities"`
}

type cityDirectoryMirrorFile struct {
	Version int           `json:"version"`
	Events  []nostr.Event `json:"events"`
}

type cityDirectoryMirrorAudit struct {
	cityDirectoryState
	MirrorCount int `json:"mirrorCount"`
}

func readStrictDirectoryFile(path string, limit int64, output any) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("city directory input requires a clean absolute path")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0022 != 0 || info.Size() <= 0 || info.Size() > limit {
		return errors.New("city directory input must be a bounded non-writable regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("invalid trailing city directory input")
	}
	return nil
}

func loadCityDirectoryAnchors(path string) (map[string]cityDirectoryAnchor, error) {
	var input cityDirectoryAnchorFile
	if err := readStrictDirectoryFile(path, 1024*1024, &input); err != nil {
		return nil, err
	}
	if input.Version != cityDirectoryVersion || len(input.Cities) < 1 || len(input.Cities) > 1000 {
		return nil, errors.New("invalid city directory anchor envelope")
	}
	anchors := make(map[string]cityDirectoryAnchor, len(input.Cities))
	for _, anchor := range input.Cities {
		if !uuidPattern.MatchString(anchor.CityID) || !lowercaseHex32(anchor.RootEventID) {
			return nil, errors.New("invalid city directory anchor")
		}
		owner, err := nostr.PubKeyFromHex(anchor.InitialOwnerPubkey)
		if err != nil || owner.Hex() != anchor.InitialOwnerPubkey {
			return nil, errors.New("invalid city directory anchor owner")
		}
		if _, exists := anchors[anchor.CityID]; exists {
			return nil, errors.New("duplicate city directory anchor")
		}
		anchors[anchor.CityID] = anchor
	}
	return anchors, nil
}

func loadCityDirectoryMirror(path string) ([]nostr.Event, error) {
	var input cityDirectoryMirrorFile
	if err := readStrictDirectoryFile(path, 8*1024*1024, &input); err != nil {
		return nil, err
	}
	if input.Version != cityDirectoryVersion || len(input.Events) < 1 || len(input.Events) > 5000 {
		return nil, errors.New("invalid city directory mirror envelope")
	}
	return input.Events, nil
}

func equalCityDirectoryStates(a, b cityDirectoryState) bool {
	return a.Version == b.Version && a.CityID == b.CityID && a.CurrentEventID == b.CurrentEventID && a.Sequence == b.Sequence && a.OwnerPubkey == b.OwnerPubkey && a.ChainLength == b.ChainLength && slices.Equal(a.OperatorPubkeys, b.OperatorPubkeys) && slices.Equal(a.RecoveryPubkeys, b.RecoveryPubkeys) && slices.Equal(a.PublicRelays, b.PublicRelays)
}

func auditCityDirectoryMirrors(anchorPath string, mirrorPaths []string, cityID string) (cityDirectoryMirrorAudit, error) {
	var result cityDirectoryMirrorAudit
	if !uuidPattern.MatchString(cityID) || len(mirrorPaths) < 2 || len(mirrorPaths) > 8 {
		return result, errors.New("city directory audit requires one city and two to eight mirrors")
	}
	anchors, err := loadCityDirectoryAnchors(anchorPath)
	if err != nil {
		return result, err
	}
	anchor, ok := anchors[cityID]
	if !ok {
		return result, errors.New("city directory has no trusted anchor")
	}
	seenPaths := map[string]bool{}
	var agreed cityDirectoryState
	for index, path := range mirrorPaths {
		if seenPaths[path] {
			return result, errors.New("duplicate city directory mirror path")
		}
		seenPaths[path] = true
		events, err := loadCityDirectoryMirror(path)
		if err != nil {
			return result, err
		}
		state, err := resolveCityDirectory(events, anchor)
		if err != nil {
			return result, err
		}
		if index == 0 {
			agreed = state
		} else if !equalCityDirectoryStates(agreed, state) {
			return result, errors.New("city directory mirrors disagree")
		}
	}
	return cityDirectoryMirrorAudit{cityDirectoryState: agreed, MirrorCount: len(mirrorPaths)}, nil
}

func cityDirectoryAuditConfigured() bool {
	return os.Getenv("RELAY_CITY_DIRECTORY_AUDIT_ANCHORS") != "" || os.Getenv("RELAY_CITY_DIRECTORY_AUDIT_MIRRORS") != "" || os.Getenv("RELAY_CITY_DIRECTORY_AUDIT_CITY") != ""
}

func runCityDirectoryAudit() error {
	anchorPath := os.Getenv("RELAY_CITY_DIRECTORY_AUDIT_ANCHORS")
	cityID := os.Getenv("RELAY_CITY_DIRECTORY_AUDIT_CITY")
	rawMirrors := os.Getenv("RELAY_CITY_DIRECTORY_AUDIT_MIRRORS")
	if anchorPath == "" || cityID == "" || rawMirrors == "" {
		return errors.New("city directory audit requires anchors, mirrors and city")
	}
	parts := strings.Split(rawMirrors, ",")
	mirrors := make([]string, 0, len(parts))
	for _, part := range parts {
		path := strings.TrimSpace(part)
		if path == "" || path != part {
			return errors.New("city directory mirror paths must be non-empty and comma-separated without whitespace")
		}
		mirrors = append(mirrors, path)
	}
	result, err := auditCityDirectoryMirrors(anchorPath, mirrors, cityID)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}
