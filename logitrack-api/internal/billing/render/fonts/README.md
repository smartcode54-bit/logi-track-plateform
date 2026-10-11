# Sarabun (built into the worker)

`Sarabun-Regular.ttf` and `Sarabun-Bold.ttf` are byte-for-byte copies of `logitrack-web/public/fonts/` (the faces the
browser embedded with jsPDF; `TestFontsMatchTheWeb` keeps them equal). The `documents.render` consumer embeds subsets
of them in the invoice, receipt and Shopee report PDFs (developer-spec.md §9.9). `PDF_FONT_DIR` points the worker at
another directory holding the same two file names.

Copyright 2018 The Sarabun Project Authors (https://github.com/cadsondemak/Sarabun). This Font Software is licensed
under the SIL Open Font License, Version 1.1 (https://openfontlicense.org), as the fonts' own name tables state; the
license permits bundling and embedding with software.
