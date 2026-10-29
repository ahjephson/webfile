# Zero-permission 7z fixture

[`zero-permissions.7z.b64`](https://github.com/ahjephson/webfile/blob/main/backend/internal/archive/testdata/zero-permissions.7z.b64)
is a base64-encoded, 174-byte synthetic 7z archive.
It contains `locked/zero.txt` with the text `readable`; it contains no user data.
Decode it before passing it to 7-Zip.

It was generated with 7-Zip 23.01 using `a -t7z -m0=Copy -mhc=off` on a folder
named `locked` containing `zero.txt`. The inline FilesInfo WinAttributes
property (`15 0a 01 00`, two defined attributes) was changed to these
little-endian values, then NextHeaderCRC and StartHeaderCRC were recomputed:

- directory: `0x40008010` (Unix directory type, zero permissions);
- file: `0x80008021` (Unix regular-file type, zero permissions, Windows
  read-only and archive flags).

`7z l -slt` reports `Attributes = RA ----------` for the file. Direct extraction
with 7-Zip 23.01 produces file mode `0000` and directory mode `0700` (7-Zip
adds directory owner access). This distinguishes zero stored Unix permissions
from Windows-only attributes: `0x00000021` alone produces a readable, read-only
file. The regression test also exercises that Windows-only variant.
