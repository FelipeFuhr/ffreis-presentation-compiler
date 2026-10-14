package seo

import (
	"encoding/json"
	"strings"
)

// linkedData is the schema.org description of a deck.
//
// `PresentationDigitalDocument` is the accurate type and is what distinguishes
// this from the fleet's websites, which describe themselves as `WebPage` and
// `WebSite`. A deck is a document that happens to be served as HTML, and
// `numberOfPages` is a property only a document type carries — so a crawler
// learns the deck's length from the markup rather than from nothing.
//
// Every field is `omitempty`: a private deck emits name, language and slide
// count and nothing else, rather than a skeleton of empty strings.
type linkedData struct {
	Context       string  `json:"@context"`
	Type          string  `json:"@type"`
	Name          string  `json:"name"`
	Description   string  `json:"description,omitempty"`
	URL           string  `json:"url,omitempty"`
	InLanguage    string  `json:"inLanguage,omitempty"`
	NumberOfPages int     `json:"numberOfPages,omitempty"`
	DatePublished string  `json:"datePublished,omitempty"`
	Keywords      string  `json:"keywords,omitempty"`
	Author        *entity `json:"author,omitempty"`
	Publisher     *entity `json:"publisher,omitempty"`

	// schema.org types `image` as ImageObject-or-URL and `thumbnailUrl` as
	// URL only. An ImageObject under thumbnailUrl is a type error that no
	// validator in this fleet would have caught, so the object goes on `image`
	// and the plain URL on `thumbnailUrl`.
	Image     *imageObject `json:"image,omitempty"`
	Thumbnail string       `json:"thumbnailUrl,omitempty"`
}

type entity struct {
	Type   string   `json:"@type"`
	Name   string   `json:"name"`
	URL    string   `json:"url,omitempty"`
	SameAs []string `json:"sameAs,omitempty"`
}

type imageObject struct {
	Type string `json:"@type"`
	URL  string `json:"url"`
	Alt  string `json:"caption,omitempty"`
}

// jsonLD renders the ld+json script block.
//
// Marshalling rather than interpolating is the whole point: a description
// containing a double quote — or any of the characters a Portuguese or
// Spanish deck title routinely carries — produces valid JSON here and a
// block no parser accepts in a hand-built template.
//
// json.Marshal escapes <, > and & to <-style sequences by default, which
// is exactly what makes the result safe to drop inside a <script> element: no
// value can close the tag early.
func jsonLD(d Document, m Meta) string {
	ld := linkedData{
		Context:       schemaContext,
		Type:          schemaType,
		Name:          strings.TrimSpace(d.Title),
		Description:   m.description(d),
		URL:           m.Canonical(),
		InLanguage:    strings.TrimSpace(d.Lang),
		NumberOfPages: d.Slides,
		DatePublished: strings.TrimSpace(m.DatePublished),
		Keywords:      strings.Join(m.Keywords, ", "),
		Author:        organization(m),
		Publisher:     organization(m),
		Image:         image(m),
		Thumbnail:     m.imageURL(),
	}

	encoded, err := json.MarshalIndent(ld, "", "  ")
	if err != nil {
		// Unreachable: every field is a string, an int or a slice of strings.
		// Emitting a broken script block would be worse than emitting none, so
		// if this ever becomes reachable it degrades to nothing.
		return ""
	}
	return "<script type=\"application/ld+json\">\n" + string(encoded) + "\n</script>\n"
}

func organization(m Meta) *entity {
	name := strings.TrimSpace(m.Organization)
	if name == "" {
		return nil
	}
	return &entity{Type: schemaOrgType, Name: name, URL: strings.TrimSpace(m.BaseURL), SameAs: m.SameAs}
}

func image(m Meta) *imageObject {
	url := m.imageURL()
	if url == "" {
		return nil
	}
	return &imageObject{Type: schemaImageType, URL: url, Alt: strings.TrimSpace(m.ImageAlt)}
}
