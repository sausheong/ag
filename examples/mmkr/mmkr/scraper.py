# mmkr/scraper.py
import os
import requests
from bs4 import BeautifulSoup
from typing import List
from urllib.parse import urljoin, urlparse
from .models import Series, Chapter, Frame

DEFAULT_HEADERS = {
    "User-Agent": "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
}


def get_series(url: str) -> Series:
    """Fetch series page and extract full chapter list via WeebCentral's API."""
    response = requests.get(url, headers=DEFAULT_HEADERS, timeout=30)
    response.raise_for_status()

    soup = BeautifulSoup(response.content, "html.parser")

    h1 = soup.find("h1")
    if h1 is None:
        raise ValueError(f"No <h1> found on page: {url}")
    name = h1.get_text(strip=True)

    # WeebCentral serves the full chapter list from a dedicated endpoint
    series_id = url.rstrip("/").split("/series/")[1].split("/")[0]
    full_list_url = f"https://weebcentral.com/series/{series_id}/full-chapter-list"
    list_response = requests.get(full_list_url, headers=DEFAULT_HEADERS, timeout=30)
    list_response.raise_for_status()

    list_soup = BeautifulSoup(list_response.content, "html.parser")

    # Chapters are <a href="/chapters/..."> links in reverse order (newest first)
    chapters: List[Chapter] = []
    chapter_links = list_soup.find_all("a", href=lambda h: h and "/chapters/" in h)
    for link in reversed(chapter_links):  # reverse to get oldest-first order
        chapter_url = link.get("href", "")
        if not chapter_url.startswith("http"):
            chapter_url = urljoin(url, chapter_url)

        # Label is the first text node in the link (strip whitespace/SVG text)
        spans = link.find_all("span")
        label = spans[0].get_text(strip=True) if spans else link.get_text(strip=True).split()[0]

        chapters.append(
            Chapter(
                number=len(chapters) + 1,
                label=label,
                title="",
                url=chapter_url,
                frames=[],
            )
        )

    return Series(name=name, url=url, chapters=chapters)


def get_chapter(chapter_url: str) -> Chapter:
    """Fetch chapter images via WeebCentral's images API endpoint."""
    # WeebCentral loads images dynamically — the /images endpoint returns
    # plain HTML with all <img alt="Page N"> tags directly accessible
    images_url = f"{chapter_url.rstrip('/')}/images?is_prev=False&current_page=1&reading_style=long_strip"
    response = requests.get(images_url, headers=DEFAULT_HEADERS, timeout=30)
    response.raise_for_status()

    soup = BeautifulSoup(response.content, "html.parser")

    frames: List[Frame] = []
    img_tags = [
        img for img in soup.find_all("img")
        if img.get("alt", "").startswith("Page ")
    ]
    for idx, img in enumerate(img_tags):
        img_url = img.get("src")
        if not img_url:
            continue

        if not img_url.startswith("http"):
            img_url = urljoin(chapter_url, img_url)

        base_filename = os.path.basename(urlparse(img_url).path)
        ext = os.path.splitext(base_filename)[1] or ".jpg"
        filename = f"{idx:04d}{ext}"

        frames.append(Frame(url=img_url, filename=filename, chapter_id=1))

    # Title extraction from the chapter page (not the images endpoint)
    title = ""
    page_response = requests.get(chapter_url, headers=DEFAULT_HEADERS, timeout=30)
    if page_response.ok:
        page_soup = BeautifulSoup(page_response.content, "html.parser")
        for tag in page_soup.find_all(["h1", "h2"]):
            text = tag.get_text(strip=True)
            if text and text.lower() not in ("", "chapter"):
                title = text
                break

    return Chapter(number=1, label="Chapter 1", title=title, url=chapter_url, frames=frames)
