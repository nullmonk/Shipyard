---
title: About
layout: home
nav_order: 0
---

<img src="assets/img/logo_alt.png" alt="Shipyard" width="700"/>  

> _quilt on steriods_

Shipyard is a tool to help build and test patches against multiple versions of a source tree. It is
designed to aid management of multiple patches that apply to multiple versions of software. The goal of Shipyard is too allow for easy and intuitive modifications to software without constantly battling reject files.


## Patchfile Creations
Shipyard provides utilities and functions for advanced patchfile creation, features include:

* Variable substitution in patchfiles
* Python code to enable patching
* Testing against many versions

## Major Limitations
* Shipyard requires code to be in a Git repo with versions tags. If you do not have version tags, you will need to create them in the source repo to use shipyard
* Shipyard does not test the _validity_ of the patches, only if they apply or not. Basic linting should be implemented on top of shipyard to ensure patches work
* Shipyard is not directly attached to a buid system, however, some tools have been provided for enabling package building
* Shipyard tries to strike a nice balance between automation and ease-of-use with customizability and error handling, sometimes this is easier said then done
