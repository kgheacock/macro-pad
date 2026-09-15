"""Renders one static HTML file to a 128x128 PNG glyph image.

Renders with WeasyPrint — which has no JavaScript engine, by design — to
a one-page PDF sized to fill the render canvas, then rasterizes that
page to a PNG with PyMuPDF (current WeasyPrint only writes PDF;
PyMuPDF's wheel bundles its own PDF engine, needing no extra system
libraries beyond what WeasyPrint itself needs). `driver/cmd/macrodriver`'s
`html` subcommand runs this script and sends the PNG it writes through
task 0030's setCustomGlyph wire path — see
tasks/ongoing/0037-render-static-html-on-a-key.md.

Usage:

    python3 tools/render_html.py <input.html> <output-path.png>

Every resource URL the input HTML names — a remote <img src>, an
@font-face, a linked stylesheet — is refused before WeasyPrint opens any
socket, so a render never depends on network access and never hangs on
an unreachable host. A <script> tag has no effect: WeasyPrint has no
JavaScript engine to run it with.
"""

import io
import sys

import fitz
from PIL import Image
from weasyprint import CSS, HTML
from weasyprint.urls import URLFetcher

CANVAS_SIZE = 128

# Fixes the rendered page to exactly CANVAS_SIZE CSS px square, with no
# margin, regardless of what the input HTML's own CSS asks for.
_PAGE_CSS = CSS(string=f"""
    @page {{ size: {CANVAS_SIZE}px {CANVAS_SIZE}px; margin: 0; }}
    html {{ margin: 0; }}
""")

# allowed_protocols=[] rejects every URL before WeasyPrint opens a
# socket for it — an <img src>, an @font-face, a linked stylesheet — so
# a render never touches the network. WeasyPrint treats a failed image
# or stylesheet fetch as a non-fatal warning and renders the rest of the
# page, so this never turns into a hang or a crash. See task 0037's
# Non-goals and DoD-3.
_blocked_fetcher = URLFetcher(allowed_protocols=[], timeout=2)


def render(input_path, output_path):
    pdf_bytes = HTML(
        filename=input_path, base_url=None, url_fetcher=_blocked_fetcher
    ).write_pdf(stylesheets=[_PAGE_CSS])

    pdf = fitz.open(stream=pdf_bytes, filetype="pdf")
    page = pdf[0]
    zoom = fitz.Matrix(CANVAS_SIZE / page.rect.width, CANVAS_SIZE / page.rect.height)
    pixmap = page.get_pixmap(matrix=zoom, alpha=False)
    rendered = Image.open(io.BytesIO(pixmap.tobytes("png"))).convert("RGB")

    # A page-size CSS rule in the input HTML can win the cascade over
    # _PAGE_CSS above, and the zoom math can round to CANVAS_SIZE +/-
    # 1px; resize as a last resort so the output is always exactly
    # CANVAS_SIZE square — transport.DecodePNGToRGBA4444 rejects any
    # other size.
    if rendered.size != (CANVAS_SIZE, CANVAS_SIZE):
        rendered = rendered.resize((CANVAS_SIZE, CANVAS_SIZE), Image.NEAREST)
    rendered.save(output_path, "PNG")


def main(argv):
    if len(argv) != 3:
        print(f"usage: {argv[0]} <input.html> <output-path.png>", file=sys.stderr)
        return 2
    render(argv[1], argv[2])
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
