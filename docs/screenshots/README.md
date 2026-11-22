# Screenshots

Taken from `web/dist` served on port 4173, with Playwright and system Chrome, after the held-out study ran:

```python
from playwright.sync_api import sync_playwright
with sync_playwright() as p:
    b = p.chromium.launch(executable_path="/usr/bin/google-chrome")
    pg = b.new_page(viewport={"width": 1440, "height": 900})
    pg.goto("http://127.0.0.1:4173/index.html"); pg.wait_for_timeout(3500)
    pg.screenshot(path="replay.png")
```
