# pack
your new least favorite package manager

pack is free software, released under the terms of the
gnu general public license, version 3 or (at your option) any later version.
this software is provided without any warranty.

copyright © 2025 shrub industries


pack is a simple package manager that uses box scripts for installation. it's designed to be straightforward - no complex dependency trees, no version hell, just packages that know how to install themselves.

## install

```bash
# clone and build
git clone https://github.com/shrub4thedub/pack.git
cd pack
go build -o pack main.go

# put it somewhere useful
./pack open pack
pack update


```

## how it works

pack downloads `.box` scripts that contain installation instructions. these scripts use the boxlang (my fucked up scripting language) to fetch source code, build it, and install binaries. it shows you and lets you edit the script before install, so no fuckery

```bash
# see what's available
pack peek edith

# install something
pack open edith

# update everything
pack update

# remove stuff
pack close edith

#wtf do i do
pack help
```

packages are verified with ed25519 signatures and cached locally. the whole thing is designed around trust-on-first-use with repository-based key distribution.

## the .pack folder

pack keeps everything organized in `~/.pack/`:

```
~/.pack/
├── store/          # actual binaries live here
│   ├── edith/
│   ├── vim/
│   └── pack/
├── locks/          # track what's installed and where it came from
├── cache/          # downloaded recipes and public keys
├── config/         # sources.box with repository urls and keys
├── local/          # local recipe overrides (no verification)
└── tmp/            # build workspace
```

when you install something, the binary goes in `store/packagename/` and gets symlinked to `~/.local/bin/`. this way you can cleanly remove packages without hunting down scattered files.

## adding sources

pack comes with a default repository, but you can add your own:

```bash
# add a new repository (automatically fetches public keys)
pack add-source https://github.com/yourname/your-pack-repo

# see configured sources
cat ~/.pack/config/sources.box
```
and you can make your own repos to host your own packages.
## writing packages

create a `.box` file that defines how to build and install your software:

```box
[data -c pkg]
  name     myapp
  desc     does something useful
  ver      latest
  src-type git
  src-url  https://github.com/you/myapp.git
  src-ref  HEAD
  bin      myapp
  license  GPL-3.0
end

[fn fetch]
  run git clone https://github.com/you/myapp.git myapp-source
  cd myapp-source
end

[fn build]
  gcc main.c -o myapp 
end

[fn install]
  env HOME
  set home ${_env_result}
  set store_dir "${home}/.pack/store/myapp"
  set bin_dir "${home}/.local/bin"
  
  # create directories
  mkdir ${store_dir}
  mkdir ${bin_dir}
  
  # install to pack store
  run cp myapp ${store_dir}/myapp
  run chmod +x ${store_dir}/myapp
  
  # create symlink in bin directory
  run ln -sf ${store_dir}/myapp ${bin_dir}/myapp
end

[main]
  fetch
  build
  install
end
```

sign it and put it in your repository with the public key in `keys/pack.pub`.

that's pretty much it.

this shit is unfinished - expect bugs
