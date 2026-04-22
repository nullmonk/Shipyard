---
title: Installation
layout: home
nav_order: 1
---

# Installation
{: .no_toc }
<details open markdown="block">
  <summary>
    Table of contents
  </summary>
  {: .text-delta }
1. TOC
{:toc}
</details>


The easiest way to install Shipyard is using pip:
```bash
pip install "git+https://github.com/nullmonk/shipyard"
```

To use Dagger-based builds (`shipyard build`), install with the full extras:
```bash
pacman -S git
pip install "shipyard[all] @ git+https://github.com/nullmonk/shipyard"
```

If you would like to develop against shipyard, use the following commands as well

```bash
git clone --depth=1 https://github.com/nullmonk/shipyard
cd shipyard
pip install -e ".[all]"
```

Ensure the pip bin is in your path, you can then run `shipyard`

{: .blue }
> Shipyard requires `git` be in the path for source control and diffing operations