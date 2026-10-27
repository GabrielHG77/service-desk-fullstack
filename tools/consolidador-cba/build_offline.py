"""Gera a versão offline (um único .html com a SheetJS embutida) a partir da página do artifact."""
import sys
from pathlib import Path

page_path, lib_path, out_path = map(Path, sys.argv[1:4])
page = page_path.read_text(encoding="utf-8")
lib = lib_path.read_text(encoding="utf-8").replace("</script", "<\\/script")

cdn_tag = '<script src="https://cdnjs.cloudflare.com/ajax/libs/xlsx/0.18.5/xlsx.full.min.js"></script>'
assert page.count(cdn_tag) == 1, "tag da SheetJS não encontrada"
head, body = page.split("<!-- /head -->", 1)
body = body.replace(cdn_tag, "<!-- SheetJS xlsx 0.18.5 (Apache-2.0), embutida para funcionar sem internet -->\n<script>" + lib + "</script>")

out = (
    "<!doctype html>\n<html lang=\"pt-BR\">\n<head>\n<meta charset=\"utf-8\">\n"
    "<meta name=\"viewport\" content=\"width=device-width,initial-scale=1,viewport-fit=cover\">\n"
    + head.strip()
    + "\n</head>\n<body>\n"
    + body.strip()
    + "\n</body>\n</html>\n"
)
out_path.parent.mkdir(parents=True, exist_ok=True)
out_path.write_text(out, encoding="utf-8")
print(f"{out_path}: {len(out.encode('utf-8')) / 1024:.0f} KB")
