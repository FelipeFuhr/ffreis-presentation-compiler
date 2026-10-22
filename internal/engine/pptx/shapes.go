// Native PowerPoint shapes: the editable half of the PPTX export.
//
// Image mode makes every slide one full-bleed picture. Fidelity is exact and
// nothing can be changed, which is wrong for anyone handed the file to work
// on. Native mode emits real PowerPoint objects instead -- text boxes you can
// retype, tables you can insert a row into, pictures you can move.
//
// The trade-off is honest and unavoidable: positions here come from the
// theme's own PPTX layout spec, not from the CSS, so native output does NOT
// match the PDF pixel for pixel. Two layout systems describe the same deck.
// That is the cost of editability, and why both modes exist rather than one
// replacing the other.
//
// A slide whose layout the theme has not described falls back to its rendered
// image, per slide. A deck is therefore allowed to be partly editable, which
// is what makes the feature useful before every layout has a spec.
package pptx

import (
	"fmt"
	"strings"
)

// Shape kinds. A shape carries only the fields its kind reads.
type ShapeKind string

const (
	// ShapeText is one text box: a title, a kicker, a caption, a paragraph.
	ShapeText ShapeKind = "text"
	// ShapeBullets is a bulleted list, one paragraph per item.
	ShapeBullets ShapeKind = "bullets"
	// ShapeTable is a real a:tbl -- rows and cells PowerPoint can edit, not a
	// grid of text boxes that only looks like a table.
	ShapeTable ShapeKind = "table"
	// ShapePicture places an image, which is what a chart or a photo stays:
	// a generated SVG has no honest mapping to native shapes, and inventing
	// one would misrepresent the data it plots.
	ShapePicture ShapeKind = "picture"
	// ShapeRect is a filled or outlined rectangle: a card, a rule, an accent
	// bar -- the theme's visual chrome.
	ShapeRect ShapeKind = "rect"
)

// Align values, as OOXML spells them.
const (
	AlignLeft   = "l"
	AlignCenter = "ctr"
	AlignRight  = "r"
)

// DefaultBullet is the glyph used when a bullets shape names none.
const DefaultBullet = "•"

// bulletIndentEMU is the hanging indent for a bulleted paragraph: 0.3125in,
// PowerPoint's own default for a first-level bullet.
const bulletIndentEMU = 285750

// Rect positions a shape on the slide, in inches from the top-left.
type Rect struct {
	XIn, YIn, WIn, HIn float64
}

// TextStyle is the run-level formatting a shape's text carries.
//
// Only what a deck actually needs: this is a declarative layout spec, not a
// reimplementation of CSS.
type TextStyle struct {
	SizePt    float64 // 0 inherits the master
	Bold      bool
	Italic    bool
	Color     string  // "RRGGBB", no leading #
	Align     string  // AlignLeft, AlignCenter, AlignRight; empty = left
	Caps      bool    // rendered via OOXML's own cap attribute, not by upper-casing the text
	SpacingPt float64 // letter spacing, for the theme's uppercase kickers
	Font      string  // typeface name; empty inherits
	LinePct   float64 // line spacing as a percentage, e.g. 120
}

// Shape is one object on a native slide.
type Shape struct {
	Kind ShapeKind
	At   Rect
	Name string // what PowerPoint shows in the selection pane

	Text   string   // ShapeText
	Items  []string // ShapeBullets
	Bullet string   // ShapeBullets; empty uses DefaultBullet

	Columns []string   // ShapeTable header row; empty means no header
	Rows    [][]string // ShapeTable body

	// RowHeightsIn and ColWidthsIn are the measured geometry of a table, in
	// document order: one height per row (header first, if there is one) and
	// one width per column. Empty divides the frame evenly, which is only
	// right for a table whose content happens to be uniform -- measuring is
	// what the caller should do, and why these exist.
	RowHeightsIn []float64
	ColWidthsIn  []float64

	PNG []byte // ShapePicture

	Style TextStyle

	// Fill and Line are "RRGGBB" or empty for none. ShapeRect uses both;
	// ShapeTable uses Fill for its header row.
	Fill string
	Line string
	// LineWidthPt is the outline width; 0 with a Line set gives a hairline.
	LineWidthPt float64
}

// ── serialisation ───────────────────────────────────────────────────────────

func emuFromPt(pt float64) int64 { return int64(pt*12700 + 0.5) }

// hundredths is the unit OOXML uses for font size and letter spacing.
func hundredths(pt float64) int64 { return int64(pt*100 + 0.5) }

func solidFill(hex string) string {
	if hex == "" {
		return ""
	}
	return fmt.Sprintf(`<a:solidFill><a:srgbClr val="%s"/></a:solidFill>`, escape(strings.TrimPrefix(hex, "#")))
}

func xfrm(r Rect) string {
	return fmt.Sprintf(`<a:xfrm><a:off x="%d" y="%d"/><a:ext cx="%d" cy="%d"/></a:xfrm>`,
		emu(r.XIn), emu(r.YIn), emu(r.WIn), emu(r.HIn))
}

// runProps renders an <a:rPr>. lang carries the deck's language so PowerPoint
// spell-checks in the right one, the same reason the notes writer sets it.
func runProps(s TextStyle, lang string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<a:rPr lang="%s"`, escape(lang))
	if s.SizePt > 0 {
		fmt.Fprintf(&b, ` sz="%d"`, hundredths(s.SizePt))
	}
	if s.Bold {
		b.WriteString(` b="1"`)
	}
	if s.Italic {
		b.WriteString(` i="1"`)
	}
	if s.SpacingPt != 0 {
		fmt.Fprintf(&b, ` spc="%d"`, hundredths(s.SpacingPt))
	}
	if s.Caps {
		// OOXML's own capitalisation, so the text a user edits is still the
		// text the deck declared -- upper-casing the string would make the
		// source and the slide disagree.
		b.WriteString(` cap="all"`)
	}
	b.WriteString(` dirty="0">`)
	b.WriteString(solidFill(s.Color))
	if s.Font != "" {
		fmt.Fprintf(&b, `<a:latin typeface="%s"/><a:cs typeface="%s"/>`, escape(s.Font), escape(s.Font))
	}
	b.WriteString(`</a:rPr>`)
	return b.String()
}

func paraProps(s TextStyle, extra string) string {
	var attrs strings.Builder
	if s.Align != "" {
		fmt.Fprintf(&attrs, ` algn="%s"`, escape(s.Align))
	}
	var inner strings.Builder
	if s.LinePct > 0 {
		fmt.Fprintf(&inner, `<a:lnSpc><a:spcPct val="%d"/></a:lnSpc>`, int64(s.LinePct*1000))
	}
	inner.WriteString(extra)
	if attrs.Len() == 0 && inner.Len() == 0 {
		return ""
	}
	return fmt.Sprintf(`<a:pPr%s>%s</a:pPr>`, attrs.String(), inner.String())
}

// para renders one paragraph. An empty line still emits a paragraph so blank
// lines in authored prose survive the round trip.
func para(text string, s TextStyle, lang, extraPr string) string {
	if text == "" {
		return fmt.Sprintf(`<a:p>%s</a:p>`, paraProps(s, extraPr))
	}
	return fmt.Sprintf(`<a:p>%s<a:r>%s<a:t>%s</a:t></a:r></a:p>`,
		paraProps(s, extraPr), runProps(s, lang), escape(text))
}

// bodyPr insets are zeroed: the theme's spec already says where the box goes,
// and PowerPoint's default 0.1in padding would shift every shape.
const bodyPrNoInset = `<a:bodyPr wrap="square" lIns="0" tIns="0" rIns="0" bIns="0" anchor="t"/><a:lstStyle/>`

func textBoxXML(id int, sh Shape, lang string) string {
	var paras strings.Builder
	for _, line := range strings.Split(sh.Text, "\n") {
		paras.WriteString(para(line, sh.Style, lang, ""))
	}
	return fmt.Sprintf(`
      <p:sp>
        <p:nvSpPr><p:cNvPr id="%d" name="%s"/><p:cNvSpPr txBox="1"/><p:nvPr/></p:nvSpPr>
        <p:spPr>%s<a:prstGeom prst="rect"><a:avLst/></a:prstGeom>%s</p:spPr>
        <p:txBody>%s%s</p:txBody>
      </p:sp>`, id, escape(shapeName(sh, "Text")), xfrm(sh.At), fillOrNone(sh.Fill), bodyPrNoInset, paras.String())
}

func bulletsXML(id int, sh Shape, lang string) string {
	glyph := sh.Bullet
	if glyph == "" {
		glyph = DefaultBullet
	}
	bullet := fmt.Sprintf(`<a:buFont typeface="Arial"/><a:buChar char="%s"/>`, escape(glyph))
	indent := fmt.Sprintf(` marL="%d" indent="-%d"`, bulletIndentEMU, bulletIndentEMU)

	var paras strings.Builder
	for _, item := range sh.Items {
		// marL/indent live on pPr's attributes, so they are spliced in rather
		// than passed through paraProps's child list.
		pr := paraProps(sh.Style, bullet)
		if pr == "" {
			pr = fmt.Sprintf(`<a:pPr%s>%s</a:pPr>`, indent, bullet)
		} else {
			pr = strings.Replace(pr, `<a:pPr`, `<a:pPr`+indent, 1)
		}
		fmt.Fprintf(&paras, `<a:p>%s<a:r>%s<a:t>%s</a:t></a:r></a:p>`,
			pr, runProps(sh.Style, lang), escape(item))
	}
	return fmt.Sprintf(`
      <p:sp>
        <p:nvSpPr><p:cNvPr id="%d" name="%s"/><p:cNvSpPr txBox="1"/><p:nvPr/></p:nvSpPr>
        <p:spPr>%s<a:prstGeom prst="rect"><a:avLst/></a:prstGeom><a:noFill/></p:spPr>
        <p:txBody>%s%s</p:txBody>
      </p:sp>`, id, escape(shapeName(sh, "List")), xfrm(sh.At), bodyPrNoInset, paras.String())
}

func rectXML(id int, sh Shape) string {
	line := `<a:ln><a:noFill/></a:ln>`
	if sh.Line != "" {
		width := ""
		if sh.LineWidthPt > 0 {
			width = fmt.Sprintf(` w="%d"`, emuFromPt(sh.LineWidthPt))
		}
		line = fmt.Sprintf(`<a:ln%s>%s</a:ln>`, width, solidFill(sh.Line))
	}
	return fmt.Sprintf(`
      <p:sp>
        <p:nvSpPr><p:cNvPr id="%d" name="%s"/><p:cNvSpPr/><p:nvPr/></p:nvSpPr>
        <p:spPr>%s<a:prstGeom prst="rect"><a:avLst/></a:prstGeom>%s%s</p:spPr>
        <p:txBody><a:bodyPr/><a:lstStyle/><a:p/></p:txBody>
      </p:sp>`, id, escape(shapeName(sh, "Rectangle")), xfrm(sh.At), fillOrNone(sh.Fill), line)
}

func pictureXML(id int, sh Shape, relID string) string {
	return fmt.Sprintf(`
      <p:pic>
        <p:nvPicPr>
          <p:cNvPr id="%d" name="%s"/>
          <p:cNvPicPr><a:picLocks noChangeAspect="1"/></p:cNvPicPr>
          <p:nvPr/>
        </p:nvPicPr>
        <p:blipFill><a:blip r:embed="%s"/><a:stretch><a:fillRect/></a:stretch></p:blipFill>
        <p:spPr>%s<a:prstGeom prst="rect"><a:avLst/></a:prstGeom></p:spPr>
      </p:pic>`, id, escape(shapeName(sh, "Picture")), escape(relID), xfrm(sh.At))
}

// tableXML emits a real a:tbl inside a graphicFrame.
//
// Cells are styled explicitly rather than left to tableStyleId: the shipped
// tableStyles.xml declares no styles of its own, so a style id would resolve
// to whatever PowerPoint's built-in default is and the deck would not look
// like the theme.
func tableXML(id int, sh Shape, lang string) string {
	cols := len(sh.Columns)
	if cols == 0 {
		for _, row := range sh.Rows {
			if len(row) > cols {
				cols = len(row)
			}
		}
	}
	if cols == 0 {
		return ""
	}

	var grid strings.Builder
	for _, w := range distribute(sh.ColWidthsIn, cols, sh.At.WIn) {
		fmt.Fprintf(&grid, `<a:gridCol w="%d"/>`, w)
	}

	rowCount := len(sh.Rows)
	if len(sh.Columns) > 0 {
		rowCount++
	}
	heights := distribute(sh.RowHeightsIn, rowCount, sh.At.HIn)

	var rows strings.Builder
	next := 0
	height := func() int64 {
		if next < len(heights) {
			h := heights[next]
			next++
			return h
		}
		return 0
	}
	if len(sh.Columns) > 0 {
		header := sh.Style
		header.Bold = true
		if sh.Fill != "" && header.Color == "" {
			header.Color = "FFFFFF"
		}
		rows.WriteString(tableRow(sh.Columns, cols, height(), header, sh.Fill, lang))
	}
	for _, row := range sh.Rows {
		rows.WriteString(tableRow(row, cols, height(), sh.Style, "", lang))
	}

	return fmt.Sprintf(`
      <p:graphicFrame>
        <p:nvGraphicFramePr>
          <p:cNvPr id="%d" name="%s"/>
          <p:cNvGraphicFramePr><a:graphicFrameLocks noGrp="1"/></p:cNvGraphicFramePr>
          <p:nvPr/>
        </p:nvGraphicFramePr>
        <p:xfrm><a:off x="%d" y="%d"/><a:ext cx="%d" cy="%d"/></p:xfrm>
        <a:graphic><a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/table">
          <a:tbl>
            <a:tblPr firstRow="1"/>
            <a:tblGrid>%s</a:tblGrid>%s
          </a:tbl>
        </a:graphicData></a:graphic>
      </p:graphicFrame>`,
		id, escape(shapeName(sh, "Table")),
		emu(sh.At.XIn), emu(sh.At.YIn), emu(sh.At.WIn), emu(sh.At.HIn),
		grid.String(), rows.String())
}

// distribute turns measured inches into EMU for n tracks of a table.
//
// Measured values are used as given, except that the last track absorbs any
// rounding so the tracks total the frame exactly -- a grid that does not sum
// to its own xfrm makes PowerPoint re-flow the table, which would undo the
// measurement. With no measurements it falls back to even division, which is
// the only thing available and is why measuring matters.
func distribute(measured []float64, n int, totalIn float64) []int64 {
	if n <= 0 {
		return nil
	}
	total := emu(totalIn)
	out := make([]int64, n)

	if len(measured) == n {
		var sum int64
		for i, v := range measured {
			out[i] = emu(v)
			sum += out[i]
		}
		if diff := total - sum; diff != 0 {
			out[n-1] += diff
		}
		return out
	}

	each := total / int64(n)
	var sum int64
	for i := 0; i < n; i++ {
		out[i] = each
		sum += each
	}
	out[n-1] += total - sum
	return out
}
func tableRow(cells []string, cols int, height int64, s TextStyle, fill, lang string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<a:tr h="%d">`, height)
	for i := 0; i < cols; i++ {
		text := ""
		if i < len(cells) {
			text = cells[i]
		}
		fmt.Fprintf(&b, `<a:tc><a:txBody><a:bodyPr/><a:lstStyle/>%s</a:txBody><a:tcPr>%s</a:tcPr></a:tc>`,
			para(text, s, lang, ""), solidFill(fill))
	}
	b.WriteString(`</a:tr>`)
	return b.String()
}

func fillOrNone(hex string) string {
	if hex == "" {
		return `<a:noFill/>`
	}
	return solidFill(hex)
}

func shapeName(sh Shape, fallback string) string {
	if sh.Name != "" {
		return sh.Name
	}
	return fallback
}
