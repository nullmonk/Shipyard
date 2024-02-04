#!/usr/bin/env python3
from setuptools import setup
from os import path

# Get Description from readme
here = path.abspath(path.dirname(__file__))
with open(path.join(here, 'README.md'), encoding='utf-8') as f:
    long_description = f.read()

setup(
    name='shipyard',
    version='0.1.4',
    description='Patchfile management',
    long_description=long_description,
    long_description_content_type='text/markdown',
    url='http://github.com/micahjmartin/shipyard',
    author='Micah Martin',
    author_email='',
    license='Apache 2.0',
    packages=['shipyard'],
    classifiers=[
        'Development Status :: 2 - Pre-Alpha',
        'Operating System :: POSIX :: Linux',
        'Topic :: Security',
        'Programming Language :: Python :: 3',
        'Programming Language :: Python :: 3.5',
        'Programming Language :: Python :: 3.6',
        'Programming Language :: Python :: 3.7',
        'Programming Language :: Python :: 3.8',
        'Programming Language :: Python :: 3.9',
        'Programming Language :: Python :: 3.10',
        'Programming Language :: Python :: 3.11',
    ],
    keywords='patch patchfile management security',
    install_requires=[
        'fire',
        'requests',
    ],
    scripts=[
        "bin/shipyard",
        "bin/shipyard-build"
    ],
    project_urls={
        'Bug Reports': 'https://github.com/micahjmartin/shipyard/issues',
        'Source': 'https://github.com/micahjmartin/shipyard/',
    },
    zip_safe=True
)