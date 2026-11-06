# TeaCat website

Static landing page for **TeaCat**, the local LLM runtime by **Tyxen**.

## Deploy with GitHub Pages

1. Create the repository `tyxteacat/teacat` on GitHub and push this project.
2. In **Settings → Pages**, choose **Deploy from a branch**, branch `main`,
   folder `/site`.
3. The page will be served at `https://tyxteacat.github.io/teacat/`.

`.nojekyll` is included so GitHub Pages serves the files as-is.

If you use a custom domain or a different account name, update:

- `<link rel="canonical">` in `index.html`
- the Open Graph / Twitter URLs
- `robots.txt`
- `sitemap.xml`
- the repository links throughout the page and in `../README.md`
- `Repository`, `Repo` and links in `../internal/brand/brand.go`

## Get indexed by Google

Publishing is not enough on its own — search engines need to discover and crawl
the site:

1. Verify the site in [Google Search Console](https://search.google.com/search-console)
   (and Bing Webmaster Tools).
2. Submit `https://tyxteacat.github.io/teacat/sitemap.xml`.
3. Request indexing for the homepage.
4. Create the GitHub repository and fill in the **About** box:
   - Description: `TeaCat — local LLM runtime by Tyxen. Run GGUF models offline with an Ollama + OpenAI compatible API, written from scratch in Go.`
   - Topics: `teacat`, `tyxen`, `local-llm`, `gguf`, `ollama`, `openai-compatible`, `golang`, `inference-engine`.
5. Link the repo from the site, and the site from the repo (already done in the
   README and footer).
6. Share the project once on relevant places (Reddit, Hacker News, X) so the
   name “TeaCat Tyxen” gets external references. Search ranking for a new name
   depends mostly on real inbound links.
7. Keep the name consistent: `TeaCat` in titles and headings, `Tyxen` as author
   in metadata and JSON-LD.

The page ships structured data (`SoftwareApplication` JSON-LD) with the name,
alternate names, author, repository and license, plus canonical, Open Graph and
Twitter metadata.
