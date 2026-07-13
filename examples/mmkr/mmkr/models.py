from dataclasses import dataclass, field
from typing import List


@dataclass
class Frame:
    url: str
    filename: str
    chapter_id: int


@dataclass
class Chapter:
    number: int
    label: str
    title: str
    url: str
    frames: List[Frame] = field(default_factory=list)


@dataclass
class Series:
    name: str
    url: str
    chapters: List[Chapter] = field(default_factory=list)
