from dataclasses import dataclass
from typing import Dict, Any

@dataclass
class Config:
    max_width: int = 1080
    quality: int = 85
    delay: float = 1.5
    max_retries: int = 3
    output_dir: str = "./output"

def load_config(cli_args: Dict[str, Any]) -> Config:
    """Load configuration with CLI argument overrides."""
    defaults = Config()

    return Config(
        max_width=cli_args.get("max_width", defaults.max_width),
        quality=cli_args.get("quality", defaults.quality),
        delay=cli_args.get("delay", defaults.delay),
        max_retries=cli_args.get("max_retries", defaults.max_retries),
        output_dir=cli_args.get("output_dir", defaults.output_dir),
    )
