# publishing.nix — Document publishing: LaTeX, typesetting, PDF generation
# Replaces apt: texlive-*, pandoc, tectonic
{pkgs, config, lib, ...}: let
  cfg = config.devcell.modules.publishing;
in {
  options.devcell.modules.publishing = {
    enable = lib.mkEnableOption "LaTeX (TeX Live medium), Tectonic, Pandoc, Typst, Marp, biber";
    meta = lib.mkOption {
      type = lib.types.attrs;
      readOnly = true;
      default = {
        description = "Document publishing: LaTeX + Pandoc + Typst + Marp slides + Biber bibliography";
        mcpServers = [ ];
        sizeMb = 1700;
      };
    };
  };

  config = lib.mkIf cfg.enable {
    home.packages = with pkgs; [
      # LaTeX — full TeX Live with common packages (science, fonts, beamer, etc.)
      # texlive.combined.scheme-medium covers 90% of use cases at ~1.5 GB.
      # Use scheme-full (~4 GB) if you need exotic packages.
      texlive.combined.scheme-medium  # LaTeX compiler + common packages (use: pdflatex, xelatex, lualatex)

      tectonic  # modern LaTeX engine — auto-downloads packages, single binary (use: tectonic doc.tex)
      pandoc    # universal document converter: md→pdf, md→docx, tex→html (use: pandoc -o out.pdf in.md)
      typst     # modern typesetting alternative to LaTeX (use: typst compile doc.typ)

      # PDF tools
      poppler-utils  # PDF utilities: pdftotext, pdfinfo, pdfunite, pdfseparate
      ghostscript    # PostScript/PDF interpreter — required by many LaTeX packages

      # Slide decks
      marp-cli  # Markdown → slide deck (HTML/PDF/PPTX) (use: marp --pdf slides.md)

      # Bibliography
      biber  # BibLaTeX backend (use: biber document)
    ];
  };
}
