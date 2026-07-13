import io
from PIL import Image
import img2pdf
import glob
import os
import shutil

from .config import Config

# JPEG encoder hard limit
_JPEG_MAX_HEIGHT = 65500
# img2pdf minimum page dimension
_MIN_DIM = 3


def resize_image(image_path: str, output_path: str, max_width: int, quality: int) -> str:
    """Resize image to max width and convert to JPEG."""
    with Image.open(image_path) as img:
        if img.width > max_width:
            ratio = max_width / img.width
            new_height = int(img.height * ratio)
            img = img.resize((max_width, new_height), Image.Resampling.LANCZOS)

        if img.mode not in ("RGB", "L"):
            img = img.convert("RGB")

        img.save(output_path, "JPEG", quality=quality)

    return output_path


def create_pdf(image_paths: list[str], output_path: str) -> str:
    """Combine images into a PDF file."""
    with open(output_path, "wb") as f:
        f.write(img2pdf.convert(image_paths))
    return output_path


def _stitch_to_pdf(imgs: list[Image.Image], quality: int) -> bytes:
    """Stitch PIL images vertically into strips and return PDF bytes.

    Images must all have the same width. Strips are kept under the JPEG
    encoder height limit so each can be saved as a single JPEG page.
    """
    width = imgs[0].width
    strips_data = []
    current, current_h = [], 0

    def flush():
        strip = Image.new("RGB", (width, current_h))
        y = 0
        for ci in current:
            strip.paste(ci, (0, y))
            y += ci.height
        buf = io.BytesIO()
        strip.save(buf, "JPEG", quality=quality)
        strips_data.append(buf.getvalue())

    for img in imgs:
        if current_h + img.height > _JPEG_MAX_HEIGHT and current:
            flush()
            current, current_h = [], 0
        current.append(img)
        current_h += img.height

    if current:
        flush()

    return img2pdf.convert(strips_data)


def process_chapter(frames_dir: str, output_path: str, config: Config) -> str:
    """Process all frames in a chapter: resize, optionally stitch, and create PDF.

    Attempts to stitch frames into seamless vertical strips. If the stitched PDF
    is larger than the per-frame PDF, falls back to the per-frame version.
    """
    image_extensions = {".png", ".jpg", ".jpeg", ".webp"}
    image_files = sorted(
        f for f in glob.glob(os.path.join(frames_dir, "*"))
        if os.path.splitext(f)[1].lower() in image_extensions
    )

    if not image_files:
        raise ValueError(f"No image files found in {frames_dir}")

    resized_dir = os.path.join(frames_dir, "resized")
    os.makedirs(resized_dir, exist_ok=True)

    resized_paths = []
    for img_path in image_files:
        base_name = os.path.splitext(os.path.basename(img_path))[0]
        output_img = os.path.join(resized_dir, f"{base_name}.jpg")
        try:
            resize_image(img_path, output_img, config.max_width, config.quality)
        except Exception:
            continue
        with Image.open(output_img) as check:
            if check.width < _MIN_DIM or check.height < _MIN_DIM:
                os.remove(output_img)
                continue
        resized_paths.append(output_img)

    # Build the per-frame PDF
    per_frame_bytes = img2pdf.convert(resized_paths)

    # Attempt seamless stitching
    try:
        imgs = [Image.open(p) for p in resized_paths]
        # Normalise widths to the first image's width
        w = imgs[0].width
        normalised = []
        for img in imgs:
            if img.mode not in ("RGB", "L"):
                img = img.convert("RGB")
            if img.width != w:
                img = img.resize((w, int(img.height * w / img.width)), Image.Resampling.LANCZOS)
            normalised.append(img)

        stitched_bytes = _stitch_to_pdf(normalised, config.quality)

        if len(stitched_bytes) <= len(per_frame_bytes):
            pdf_bytes = stitched_bytes
        else:
            pdf_bytes = per_frame_bytes
    except Exception:
        pdf_bytes = per_frame_bytes

    with open(output_path, "wb") as f:
        f.write(pdf_bytes)

    shutil.rmtree(resized_dir, ignore_errors=True)
    return output_path
