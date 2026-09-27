package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"fiatjaf.com/nostr"
)

const contentPageKind nostr.Kind = 30307
const homePageID = "00000000-0000-4000-8000-000000000001"

var contentSlugPattern = regexp.MustCompile(`^(?:[a-z0-9]+(?:-[a-z0-9]+)*)?$`)
var reservedContentSlugs = map[string]bool{"admin": true, "api": true, "organizer": true, "pilot": true, "preview": true, "start": true}

type contentPage struct {
	PageID             string `json:"pageId"`
	Slug               string `json:"slug"`
	Title              string `json:"title"`
	Eyebrow            string `json:"eyebrow"`
	Intro              string `json:"intro"`
	Body               string `json:"body"`
	CTALabel           string `json:"ctaLabel"`
	CTAHref            string `json:"ctaHref"`
	FooterTitle        string `json:"footerTitle"`
	FooterText         string `json:"footerText"`
	FooterCTALabel     string `json:"footerCtaLabel"`
	Published          bool   `json:"published"`
	PreviousRevisionID string `json:"previousRevisionId,omitempty"`
}

func contentHrefOK(value string) bool {
	if value == "" {
		return true
	}
	if strings.HasPrefix(value, "#") {
		return len(value) > 1 && len(value) <= 500
	}
	if strings.HasPrefix(value, "/") {
		return !strings.HasPrefix(value, "//") && len(value) <= 500
	}
	u, err := url.Parse(value)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && len(value) <= 500
}

func parseContentPage(event nostr.Event) (contentPage, error) {
	var page contentPage
	decoder := json.NewDecoder(bytes.NewBufferString(event.Content))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&page) != nil {
		return page, errors.New("invalid: content page JSON required")
	}
	d, de := uniqueTag(event, "d")
	indexed, ie := uniqueTag(event, "i")
	slugTag, pe := uniqueTag(event, "page")
	status, se := uniqueTag(event, "status")
	client, ce := uniqueTag(event, "client")
	if de != nil || ie != nil || pe != nil || se != nil || ce != nil || !uuidPattern.MatchString(page.PageID) || !workflowAddress(d, page.PageID) || indexed != page.PageID || slugTag != map[bool]string{true: "/", false: page.Slug}[page.Slug == ""] || client != "bitcoinwalk.org" {
		return page, errors.New("invalid: content page tags")
	}
	if status != map[bool]string{true: "published", false: "unpublished"}[page.Published] || !contentSlugPattern.MatchString(page.Slug) || len(page.Slug) > 80 || reservedContentSlugs[page.Slug] || page.PageID == homePageID && page.Slug != "" || page.PageID != homePageID && page.Slug == "" {
		return page, errors.New("invalid: content page URL or status")
	}
	if !sizeOK(strings.TrimSpace(page.Title), 1, 160) || !sizeOK(page.Eyebrow, 0, 160) || !sizeOK(page.Intro, 0, 1200) || !sizeOK(page.Body, 0, 30000) || !sizeOK(page.CTALabel, 0, 100) || !sizeOK(page.FooterTitle, 0, 160) || !sizeOK(page.FooterText, 0, 1200) || !sizeOK(page.FooterCTALabel, 0, 100) || !contentHrefOK(page.CTAHref) {
		return page, errors.New("invalid: content page fields")
	}
	previousTags := 0
	previousID := ""
	allowed := map[string]bool{"d": true, "i": true, "page": true, "status": true, "client": true, "e": true}
	for _, tag := range event.Tags {
		if len(tag) < 2 || !allowed[tag[0]] {
			return page, errors.New("restricted: unsupported content tag")
		}
		if tag[0] == "e" {
			if len(tag) != 4 || tag[2] != "" || tag[3] != "previous" {
				return page, errors.New("invalid: content previous reference")
			}
			previousTags++
			previousID = tag[1]
		} else if len(tag) != 2 {
			return page, fmt.Errorf("invalid: malformed %s tag", tag[0])
		}
	}
	if (page.PreviousRevisionID == "") != (previousTags == 0) || previousTags > 1 || previousID != page.PreviousRevisionID {
		return page, errors.New("invalid: content previous reference mismatch")
	}
	return page, nil
}

func (p *organizerPolicy) latestContent(pageID string) *nostr.Event {
	var latest *nostr.Event
	for event := range p.db.QueryEvents(nostr.Filter{Kinds: []nostr.Kind{contentPageKind}, Authors: []nostr.PubKey{p.admin}, Tags: nostr.TagMap{"i": []string{pageID}}}, 10001) {
		copy := event
		if latest == nil || copy.CreatedAt > latest.CreatedAt || copy.CreatedAt == latest.CreatedAt && copy.ID.Hex() > latest.ID.Hex() {
			latest = &copy
		}
	}
	return latest
}

func (p *organizerPolicy) latestByIndexed(kind nostr.Kind, indexed string) *nostr.Event {
	var latest *nostr.Event
	for event := range p.db.QueryEvents(nostr.Filter{Kinds: []nostr.Kind{kind}, Authors: []nostr.PubKey{p.admin}, Tags: nostr.TagMap{"i": []string{indexed}}}, 10001) {
		copy := event
		if latest == nil || copy.CreatedAt > latest.CreatedAt || copy.CreatedAt == latest.CreatedAt && copy.ID.Hex() > latest.ID.Hex() {
			latest = &copy
		}
	}
	return latest
}
func (p *organizerPolicy) contentSlugInUse(slug, pageID string) bool {
	latest := map[string]nostr.Event{}
	count := 0
	for event := range p.db.QueryEvents(nostr.Filter{Kinds: []nostr.Kind{contentPageKind}, Authors: []nostr.PubKey{p.admin}}, 10001) {
		count++
		page, err := parseContentPage(event)
		if err != nil {
			continue
		}
		old, ok := latest[page.PageID]
		if !ok || event.CreatedAt > old.CreatedAt || event.CreatedAt == old.CreatedAt && event.ID.Hex() > old.ID.Hex() {
			latest[page.PageID] = event
		}
	}
	if count > 10000 {
		return true
	}
	for id, event := range latest {
		if id == pageID {
			continue
		}
		page, err := parseContentPage(event)
		if err == nil && page.Published && page.Slug == slug {
			return true
		}
	}
	return false
}
func (p *organizerPolicy) citySlugExists(slug string) bool {
	count := 0
	for event := range p.db.QueryEvents(nostr.Filter{Kinds: []nostr.Kind{30303}}, 10001) {
		count++
		city, err := parseDraft(event)
		if err == nil && city.Slug == slug {
			return true
		}
	}
	return count > 10000
}
func (p *organizerPolicy) checkContent(event nostr.Event) error {
	if event.PubKey != p.admin {
		return errors.New("restricted: only the super-admin can publish content")
	}
	page, err := parseContentPage(event)
	if err != nil {
		return err
	}
	current := p.latestContent(page.PageID)
	if current == nil && page.PreviousRevisionID != "" {
		return errors.New("invalid: first content revision cannot reference a predecessor")
	}
	if current != nil && (page.PreviousRevisionID != current.ID.Hex() || event.CreatedAt <= current.CreatedAt) {
		return errors.New("restricted: content was changed; reload before publishing")
	}
	if p.contentSlugInUse(page.Slug, page.PageID) || page.Slug != "" && p.citySlugExists(page.Slug) {
		return errors.New("restricted: content URL is already in use")
	}
	return nil
}
