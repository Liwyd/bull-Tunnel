package web

import (
	"fmt"
	"html/template"
	"strings"
)

/* The pieces of the network view that go into a page. The panel renders
   them: the page holds the dashboard before the script runs, and the script
   draws the same view over it when the state changes. */

// NodesHTML is the cards of the canvas, as the page holds them.
func (v *NetworkView) NodesHTML() template.HTML {
	parts := []string{}
	for _, node := range v.Nodes {
		parts = append(parts, fmt.Sprintf(`<div class="%s" data-id="%s" data-type="%s" data-col="%s" `+
			`style="left:%.0fpx;top:%.0fpx;height:%dpx">%s</div>`,
			escape(node.Class), escape(node.ID), escape(node.Type), escape(node.Col),
			node.X, node.Y, node.Height, node.HTML))
	}
	return template.HTML(strings.Join(parts, "\n"))
}

// ColumnsHTML is the titles above the columns of the canvas.
func (v *NetworkView) ColumnsHTML() template.HTML {
	parts := []string{}
	for _, column := range v.Columns {
		parts = append(parts, fmt.Sprintf(`<div class="ct" data-col="%s" style="left:%.0fpx;top:%.0fpx;width:%dpx">`+
			`<span class="n">%02d / %s</span><span class="t">%s</span></div>`,
			escape(column.Col), column.X, column.Y, column.Width,
			column.Count, escape(column.Kicker), escape(column.Title)))
	}
	return template.HTML(strings.Join(parts, "\n"))
}

// StripHTML is the numbers at the top of the page.
func (v *NetworkView) StripHTML() template.HTML {
	parts := []string{}
	for _, cell := range v.Strip {
		parts = append(parts, fmt.Sprintf(`<div><span class="k">%s</span><span class="v %s">%s</span></div>`,
			escape(cell.Key), escape(cell.Class), escape(cell.Value)))
	}
	return template.HTML(strings.Join(parts, "\n"))
}
