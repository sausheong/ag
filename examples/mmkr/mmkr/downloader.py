import requests
import time
import os
import json
from typing import List, Dict, Any
from tqdm import tqdm
from .models import Frame
from .config import Config


def download_single(url: str, output_path: str, max_retries: int = 3) -> bool:
    """Download a single image with retry logic and exponential backoff.

    Args:
        url: The URL of the image to download.
        output_path: The local file path to save the downloaded image.
        max_retries: Maximum number of attempts before giving up.

    Returns:
        True on success, False if all retries are exhausted.
    """
    for attempt in range(max_retries):
        try:
            response = requests.get(url, timeout=30)
            response.raise_for_status()

            os.makedirs(os.path.dirname(output_path), exist_ok=True) if os.path.dirname(output_path) else None

            with open(output_path, "wb") as f:
                f.write(response.content)

            return True
        except OSError:
            raise  # disk full, permission denied - don't retry
        except Exception:
            if attempt < max_retries - 1:
                time.sleep(2 ** attempt)
            continue

    return False


def download_frames(frames: List[Frame], output_dir: str, config: Config) -> List[str]:
    """Download all frames with progress tracking."""
    os.makedirs(output_dir, exist_ok=True)
    downloaded = []

    for frame in tqdm(frames, desc="Downloading frames"):
        output_path = os.path.join(output_dir, frame.filename)

        if download_single(frame.url, output_path, config.max_retries):
            downloaded.append(output_path)

        if config.delay > 0:
            time.sleep(config.delay)

    return downloaded


def load_progress(progress_file: str) -> Dict[str, Any]:
    """Load progress state from JSON file."""
    if not os.path.exists(progress_file):
        return {
            "series": "",
            "completed_chapters": [],
            "current_chapter": 0,
            "downloaded_frames": {}
        }

    with open(progress_file, "r") as f:
        return json.load(f)


def save_progress(progress_file: str, state: Dict[str, Any]):
    """Save progress state to JSON file."""
    with open(progress_file, "w") as f:
        json.dump(state, f, indent=2)
