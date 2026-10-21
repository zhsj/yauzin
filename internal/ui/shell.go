package ui

import (
	"gioui.org/layout"
)

// shell draws the Gmail frame: a top bar and a two column body whose right side
// holds a single rounded card. The card switches between the conversation list
// and the reader, so the app never shows more than two columns. withTop is false
// when the caller already drew the header as the title bar above the overlays.
func (a *App) shell(gtx layout.Context, withTop bool) layout.Dimensions {
	height := gtx.Dp(a.headerHeight())
	children := make([]layout.FlexChild, 0, 2)
	if withTop {
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.Y = height
			gtx.Constraints.Max.Y = height
			return a.top.Layout(gtx)
		}))
	}
	children = append(children,
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return a.body(gtx)
		}),
	)
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}

func (a *App) body(gtx layout.Context) layout.Dimensions {
	children := make([]layout.FlexChild, 0, 2)
	if !a.sidebarCollapsed {
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			w := gtx.Dp(sidebarWidth)
			gtx.Constraints.Min.X = w
			gtx.Constraints.Max.X = w
			return a.side.Layout(gtx)
		}))
	}
	children = append(children, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
		return a.content(gtx)
	}))
	return layout.Flex{Axis: layout.Horizontal}.Layout(gtx, children...)
}

// content is the white card holding either the list or the reader.
func (a *App) content(gtx layout.Context) layout.Dimensions {
	return layout.Inset{Top: 2, Right: 8, Bottom: 8, Left: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Stack{Alignment: layout.NW}.Layout(gtx,
			layout.Expanded(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min = gtx.Constraints.Max
				return fillRect(gtx, colSurface, 16)
			}),
			layout.Expanded(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: 4, Right: 8, Bottom: 4, Left: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					if a.selectedThread != "" {
						return a.reader.Layout(gtx)
					}
					return a.list.Layout(gtx)
				})
			}),
		)
	})
}
