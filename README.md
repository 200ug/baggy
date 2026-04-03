# baggy

<img src="./docs/baggy.png" align="right" height="80">

SFTP wrapper to perform stateful and E2EE file synchronization with a single `sync` command.

- Sync state tracking via metadata files
- Simplified 3-way merge conflict resolution
- Password-based encryption (Argon2 -> AES-256-GCM)
- "Smart" file exclusion with unified pattern & magic byte matching

## usage

If you're using a non-standard server SSH port and don't yet have entries in `known_hosts` file, run the following command to generate them (otherwise the client won't detect the keys properly, and the initialization checks will always fail):

```
ssh-keyscan -p <port> <hostname> >> ~/.ssh/known_hosts
```

A new configuration (stored to `~/.config/baggy.conf`) can be created like this:

```
baggy init -key <privkey_path> -remote <user>@<hostname>:<port>:<remote_storage_root>
```

After the initial config creation running `baggy sync` in a directory will begin the sync process (whether or not the directory has been synced to the remote before). Notably the tool (intentionally) utilizes a flat remote directory structure, which means there's a risk of mixing different directories if they're named the same (e.g. `path1/dir` and `path2/dir`).

---

###### Mirrors: [Codeberg](https://codeberg.org/2ug/baggy) / [Github](https://github.com/200ug/baggy)
