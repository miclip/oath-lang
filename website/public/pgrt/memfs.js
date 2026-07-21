// Synchronous in-memory filesystem for Go's js/wasm runtime. Every method
// invokes its callback INLINE — that is the whole point: Go's fsCall parks on a
// buffered channel, so an inline callback lets the goroutine resume without the
// (blocked) JS event loop ever running. Async fs deadlocks; this does not.
'use strict';
export const C = { O_RDONLY:0, O_WRONLY:1, O_RDWR:2, O_CREAT:64, O_EXCL:128, O_TRUNC:512,
  O_APPEND:1024, O_SYNC:1052672, O_DIRECTORY:65536 };
const S_IFDIR = 0o40000, S_IFREG = 0o100000;
function enoent(){ const e=new Error('ENOENT'); e.code='ENOENT'; return e; }

class MemFS {
  constructor(init){ // init: { '/abs/path': Uint8Array | 'DIR' }
    this.files = new Map(); this.fds = new Map(); this.nextFd = 3;
    for (const [p,v] of Object.entries(init||{}))
      this.files.set(p, v === 'DIR' ? {dir:true} : {dir:false, data:Uint8Array.from(v)});
    this.constants = C;
  }
  _stat(node){ return {
    isDirectory:()=>!!node.dir, isFile:()=>!node.dir,
    mode: (node.dir?S_IFDIR:S_IFREG)|0o644, size: node.dir?0:node.data.length,
    dev:1, ino:1, nlink:1, uid:0, gid:0, rdev:0, blksize:4096,
    blocks: node.dir?0:Math.ceil(node.data.length/512),
    atimeMs:0, mtimeMs:0, ctimeMs:0 };
  }
  open(p,flags,mode,cb){ let n=this.files.get(p);
    if(!n){ if(!(flags&C.O_CREAT)) return cb(enoent()); n={dir:false,data:new Uint8Array(0)}; this.files.set(p,n); }
    else if(flags&C.O_TRUNC && !n.dir) n.data=new Uint8Array(0);
    const fd=this.nextFd++; this.fds.set(fd,{p,pos:0,append:!!(flags&C.O_APPEND)}); cb(null,fd); }
  close(fd,cb){ this.fds.delete(fd); cb(null); }
  fstat(fd,cb){ const f=this.fds.get(fd); const n=f&&this.files.get(f.p); if(!n) return cb(enoent()); cb(null,this._stat(n)); }
  stat(p,cb){ const n=this.files.get(p); if(!n) return cb(enoent()); cb(null,this._stat(n)); }
  lstat(p,cb){ this.stat(p,cb); }
  read(fd,buf,off,len,position,cb){ const f=this.fds.get(fd); const n=this.files.get(f.p);
    const pos = position===null||position===undefined ? f.pos : position;
    const src = n.data.subarray(pos, pos+len); buf.set(src, off);
    if(position===null||position===undefined) f.pos += src.length; cb(null, src.length); }
  write(fd,buf,off,len,position,cb){ const f=this.fds.get(fd); const n=this.files.get(f.p);
    const chunk = buf.subarray(off, off+len);
    let pos = f.append ? n.data.length : (position===null||position===undefined ? f.pos : position);
    const end = pos+chunk.length;
    if(end > n.data.length){ const g=new Uint8Array(end); g.set(n.data); n.data=g; }
    n.data.set(chunk, pos); if(position===null||position===undefined) f.pos = pos+chunk.length; cb(null, chunk.length); }
  mkdir(p,mode,cb){ if(!this.files.has(p)) this.files.set(p,{dir:true}); cb(null); }
  rename(a,b,cb){ const n=this.files.get(a); if(!n) return cb(enoent()); this.files.delete(a); this.files.set(b,n); cb(null); }
  unlink(p,cb){ this.files.delete(p); cb(null); }
  rmdir(p,cb){ this.files.delete(p); cb(null); }
  readdir(p,cb){ const pre=p.endsWith('/')?p:p+'/'; const out=new Set();
    for(const k of this.files.keys()) if(k.startsWith(pre)){ const rest=k.slice(pre.length).split('/')[0]; if(rest) out.add(rest); }
    cb(null,[...out]); }
  fsync(fd,cb){ cb(null); } ftruncate(fd,len,cb){ const f=this.fds.get(fd); const n=this.files.get(f.p); n.data=n.data.subarray(0,len); cb(null); }
  chmod(p,m,cb){ cb(null); } fchmod(fd,m,cb){ cb(null); } chown(p,u,g,cb){ cb(null); } fchown(fd,u,g,cb){ cb(null); }
  lchown(p,u,g,cb){ cb(null); } utimes(p,a,m,cb){ cb(null); } link(a,b,cb){ cb(null); } symlink(a,b,cb){ cb(null); }
  readlink(p,cb){ cb(enoent()); } truncate(p,l,cb){ cb(null); }
  // Go's runtime writes stdout(1)/stderr(2) through writeSync. Route to the
  // console, buffered by line, so this works in a browser worker (no `process`)
  // as well as in Node. The prover prints progress here; check() does not.
  writeSync(fd,buf){
    this._dec = this._dec || new TextDecoder();
    this._buf = (this._buf || '') + this._dec.decode(buf, {stream:true});
    let i; while((i=this._buf.indexOf('\n'))>=0){ const line=this._buf.slice(0,i); this._buf=this._buf.slice(i+1);
      if(fd===2) console.error(line); else console.log(line); }
    return buf.length;
  }
}
export { MemFS };
