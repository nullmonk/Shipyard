---
title: Installation
layout: home
nav_order: 1
---

## Installation
The easiest way to install Shipyard is by using pip and your package manager:
```bash
pacman -S git
pip install git+https://github.com/micahjmartin/shipyard
```

If you would like to develop against shipyard, use the following commands as well

```bash
pacman -S git python
pip -r requirements.txt
git clone --depth=1 https://github.com/micahjmartin/shipyard
cd shipyard
pip install -e .
```

Ensure the pip bin is in your path, you can then run `shipyard`

{: .blue }
> Shipyard requires `git` be in the path for source control and diffing operations