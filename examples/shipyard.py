#!/usr/bin/env python3
import os
import sys

# Add the parent directory to sys.path to import shipyard
sys.path.append(os.path.join(os.path.dirname(__file__), ".."))

from shipyard.cli import ShipyardCLI

# Configuration
# List of (directory, package_name) tuples
PROJECTS = [
    ("examples/proftpd", "proftpd"),
    # Add other examples here, e.g. ("examples/hello", "hello")
]

# List of images to build on
IMAGES = [
    "debian:bookworm",
    "rockylinux:9",
    "archlinux:base-devel"
]

def build_project(project_dir, package, image):
    print(f"\n{'='*60}")
    print(f"Building {package} in {project_dir} on {image}")
    print(f"{ '='*60}\n")
    
    # Change to project directory so ShipyardCLI finds the shipfile
    original_cwd = os.getcwd()
    try:
        os.chdir(project_dir)
        cli = ShipyardCLI(directory=".")
        # We need to manually call the async build logic or use the cli wrapper if possible.
        # ShipyardCLI.build uses anyio.run internally, so we can just call it.
        # Note: output_dir is hardcoded in CLI to "build-output", which will be relative to project_dir
        
        # We catch SystemExit because fire/CLI might exit on error, 
        # but ShipyardCLI.build just prints and exits on some errors. 
        # Actually ShipyardCLI.build calls exit(1) on import error or export error.
        # We'll try to run it.
        
        cli.build(image=image, package=package)
        
    except SystemExit as e:
        if e.code != 0:
            print(f"[-] Build failed for {package} on {image} with exit code {e.code}")
        else:
             print(f"[+] Build finished for {package} on {image}")
    except Exception as e:
        print(f"[-] An error occurred: {e}")
    finally:
        os.chdir(original_cwd)

def main():
    base_dir = os.getcwd()
    
    for project_dir, package in PROJECTS:
        # Resolve absolute path for project dir
        abs_project_dir = os.path.join(base_dir, project_dir)
        
        if not os.path.exists(abs_project_dir):
            print(f"[!] Project directory not found: {abs_project_dir}")
            continue

        for image in IMAGES:
            build_project(abs_project_dir, package, image)

if __name__ == "__main__":
    main()
