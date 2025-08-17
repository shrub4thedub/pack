# pack
your new least favorite package manager

pack is free software, provided to you at no charge under the terms of the
gnu general public license, version 3 or (at your option) any later version.
this software is provided without any warranty.

copyright © 2025 shrub industries.

pack is a simple package manager that uses [box](https://github.com/shrub4thedub/boxlang) scripts for installation. it's designed to be straightforward; no complex dependency trees, no version hell, simply packages that know how to install themselves based on a readable, auditable script. its easy to make your own pack repositories to distribute your own software too.

pack features automatic ed25519 signature verification, repository-based key management with automatic rotation, and zero-touch security updates. all packages are verified before installation and keys are automatically cached and managed.

## install
pack is easy and fun to install. try it:

```bash
# clone and build
git clone https://github.com/shrub4thedub/pack.git
cd pack
go build -o pack main.go

# put everything in it's right place
./pack bootstrap

```

## how it works

pack downloads `.box` scripts that contain installation instructions. these scripts use the boxlang (my fucked up scripting language) to fetch source code, build it, and install binaries. it shows you and lets you edit the script before install, so no fuckery

```bash
# check package details
pack peek <pkg>

# install something
pack open <pkg>

# update everything
pack update

# remove stuff
pack close <pkg>

# see what's installed
pack shelf

# see all available packages
pack list

# search for packages
pack seek <term>

#wtf do i do
pack help

#seriously wtf do i do
pack <cmd> help
```

packages are verified with ed25519 signatures and cached locally. the whole thing is designed around trust-on-first-use with repository-based key distribution.

## the .pack folder

pack keeps everything organized in `~/.pack/`:

```
~/.pack/
├── shelf/          # actual binaries live here
│   ├── edith/
│   ├── vim/
│   └── pack/
├── locks/          # track what's installed and where it came from
├── cache/          # downloaded recipes and public keys
├── config/         # sources.box with repository urls and keys
├── local/          # local recipe overrides (no verification)
└── tmp/            # build workspace
```

when you install something, the binary goes in `shelf/packagename/` and gets symlinked to `~/.local/bin/`. this way you can cleanly remove packages without hunting down scattered files.

## adding sources

pack comes with a default repository, but you can add your own:

```bash
# add a new repository (automatically fetches public keys)
pack add-source https://github.com/yourname/your-pack-repo

# see configured sources
cat ~/.pack/config/sources.box
```
and you can make your own repos to host your own packages.
alternatively, you can provide .box files and copy them into your `~/.pack/local`
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
  set shelf_dir "${home}/.pack/shelf/myapp"
  set bin_dir "${home}/.local/bin"
  
  # create directories
  mkdir ${shelf_dir}
  mkdir ${bin_dir}
  
  # install to pack shelf
  run cp myapp ${shelf_dir}/myapp
  run chmod +x ${shelf_dir}/myapp
  
  # create symlink in bin directory
  run ln -sf ${shelf_dir}/myapp ${bin_dir}/myapp
end

[main]
  fetch
  build
  install
end
```

sign it and put it in your repository with the public key in `keys/pack.box`.

that's pretty much it.

this shit is unfinished - expect bugs
