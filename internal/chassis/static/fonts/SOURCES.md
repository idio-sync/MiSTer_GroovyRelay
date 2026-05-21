# Chassis Font Sources

Reproducibility manifest for the woff2 files committed alongside
`internal/chassis/`. Update both file and checksum when bumping
to a new upstream release.

## DSEG (8 files)

Source: https://github.com/keshikan/DSEG
Release: v0.46
License: MIT (see LICENSE)
Download URL: https://github.com/keshikan/DSEG/releases/download/v0.46/fonts-DSEG_v046.zip

| Local filename | SHA-256 |
|---|---|
| DSEG7Classic-Regular.woff2 | 8a61b7dbc89367dbc0face2541ed69a2bf0cc05b23d1064f670284ab61044481 |
| DSEG7Classic-Bold.woff2 | ec2e7499bc8ac8f8225e1fb6a5d45ff6083c6e2b0efbaf99d37fa7b42a5767ff |
| DSEG7Modern-Regular.woff2 | 3ea4cfd8ce47d00563bf967151cea3a4be9109edb49d38d8b48f98fe5abf1780 |
| DSEG7Modern-Bold.woff2 | 05eab7015f3aaba2ffc32b4d29362f9b470e1a4afe8d2c8332d301bb061a83cf |
| DSEG14Classic-Regular.woff2 | 327c24d55049422f513d59fddc3919bba72186ea6b42ea8e214be06e0faa0530 |
| DSEG14Classic-Bold.woff2 | a73df6e66512d797a78377bb5e8367fe61537107fb63cd50452d6c5aad4eae7b |
| DSEG14Modern-Regular.woff2 | f75bb21980b68acc537f2ce7592be694d466d888adf185a0d5d9f3b34352987c |
| DSEG14Modern-Bold.woff2 | 7d5b08c7de47886ee688b8cc0546d8edf89cc05a2624ae7c834f02162e5d1e71 |

## Inter

Source: https://github.com/rsms/inter
Release: v4.0
License: SIL Open Font License 1.1 (see LICENSE)
Download URL: https://github.com/rsms/inter/releases/download/v4.0/Inter-4.0.zip
Zip entry: web/InterVariable.woff2

| Local filename | SHA-256 |
|---|---|
| Inter-Variable.woff2 | 8af7bd5b545567adffb3dfceb5bedb353a522d7bf1b3a2b8af7b6064156babc0 |

## Verification

```bash
cd internal/chassis/static/fonts/
sha256sum -c <(grep -E '\| \S+\.woff2 \|' SOURCES.md | awk -F'[ |]+' '{ print $4 "  " $3 }')
```

PowerShell verification:

```powershell
$expected = @{}
Get-Content internal/chassis/static/fonts/SOURCES.md |
  Where-Object { $_ -match '^\| .*\.woff2 \|' } |
  ForEach-Object {
    $parts = $_ -split '\|'
    $expected[$parts[1].Trim()] = $parts[2].Trim().ToLowerInvariant()
  }
foreach ($name in $expected.Keys) {
  $actual = (Get-FileHash -Algorithm SHA256 -LiteralPath "internal/chassis/static/fonts/$name").Hash.ToLowerInvariant()
  if ($actual -ne $expected[$name]) { throw "$name checksum mismatch: $actual != $($expected[$name])" }
}
```
