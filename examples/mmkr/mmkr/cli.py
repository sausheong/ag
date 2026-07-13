import typer
import logging

app = typer.Typer(help="Manga downloader and PDF generator")


def parse_chapter_range(chapter_range: str) -> tuple[int, int]:
    """Parse chapter range string like '1-50' into start and end."""
    parts = chapter_range.split("-")
    if len(parts) != 2:
        raise ValueError(f"Invalid chapter range: {chapter_range}")
    try:
        start = int(parts[0])
        end = int(parts[1])
    except ValueError:
        raise ValueError(f"Invalid chapter range: {chapter_range}")
    if start > end:
        raise ValueError(f"Invalid chapter range: start ({start}) > end ({end})")
    return start, end


def setup_logging(verbose: bool):
    level = logging.DEBUG if verbose else logging.INFO
    logging.basicConfig(
        level=level,
        format="%(asctime)s - %(name)s - %(levelname)s - %(message)s",
    )


@app.command()
def main(
    url: str = typer.Argument(help="Series URL from WeebCentral"),
    chapters: str = typer.Option(..., help="Chapter range, e.g., '1-50'"),
    output: str = typer.Option("./output", help="Output directory"),
    resume: bool = typer.Option(False, help="Resume from last progress"),
    fresh: bool = typer.Option(False, help="Start fresh, delete progress file"),
    verbose: bool = typer.Option(False, help="Enable debug logging"),
    max_width: int = typer.Option(1080, help="Max image width in pixels"),
    quality: int = typer.Option(85, help="JPEG quality 1-100"),
    delay: float = typer.Option(1.5, help="Delay between requests in seconds"),
    max_retries: int = typer.Option(3, help="Max retry attempts"),
):
    """Download manga chapters and generate PDFs."""
    import os
    import shutil
    import traceback
    from .scraper import get_series, get_chapter
    from .downloader import download_frames, load_progress, save_progress
    from .processor import process_chapter
    from .config import load_config

    setup_logging(verbose)

    try:
        start_chapter, end_chapter = parse_chapter_range(chapters)

        config = load_config({
            "max_width": max_width,
            "quality": quality,
            "delay": delay,
            "max_retries": max_retries,
            "output_dir": output,
        })

        typer.echo("Fetching series info...")
        series = get_series(url)
        typer.echo(f"Found series: {series.name}")

        selected_chapters = series.chapters[start_chapter - 1:end_chapter]
        typer.echo(f"Processing {len(selected_chapters)} chapters ({start_chapter}-{end_chapter})")

        series_dir = os.path.join(output, series.name)
        os.makedirs(series_dir, exist_ok=True)

        progress_file = os.path.join(series_dir, ".mmkr_progress.json")
        if fresh and os.path.exists(progress_file):
            os.remove(progress_file)

        progress = load_progress(progress_file) if resume else {}

        for chapter in selected_chapters:
            if resume and chapter.number in progress.get("completed_chapters", []):
                typer.echo(f"Skipping chapter {chapter.number} (already completed)")
                continue

            typer.echo(f"\nProcessing: {chapter.label}")

            chapter_data = get_chapter(chapter.url)

            frames_dir = os.path.join(series_dir, f"ch{chapter.number:03d}_frames")
            download_frames(chapter_data.frames, frames_dir, config)

            pdf_filename = f"ch{chapter.number:03d}.pdf"
            if chapter_data.title:
                safe_title = "".join(c if c.isalnum() else "_" for c in chapter_data.title)
                pdf_filename = f"ch{chapter.number:03d}_{safe_title}.pdf"

            pdf_path = os.path.join(series_dir, pdf_filename)
            process_chapter(frames_dir, pdf_path, config)
            shutil.rmtree(frames_dir, ignore_errors=True)

            typer.echo(f"Created: {pdf_path}")

            if "completed_chapters" not in progress:
                progress["completed_chapters"] = []
            progress["completed_chapters"].append(chapter.number)
            save_progress(progress_file, progress)

        typer.echo(f"\nDone! Processed {len(selected_chapters)} chapters.")

    except Exception as e:
        typer.echo(f"Error: {e}", err=True)
        if verbose:
            traceback.print_exc()
        raise typer.Exit(1)
