# Notices

cpython-go's own code (generator, tooling, libc supplements, the `cpython`
embedding package) is MIT licensed, see LICENSE.

## CPython

libpython/ and stdlib/ are a derivative work of CPython 3.14, licensed under
the Python Software Foundation License Version 2 (LICENSE-CPYTHON), which
requires a brief summary of the changes made to Python:

- The C sources are transpiled to Go with modernc.org/ccgo (no C compiler is
  used at build time of the Go module); all extension modules are linked
  statically; dynamic loading, `zlib`, `_ssl`, `_sqlite3`, `_ctypes`, `_bz2`,
  `_lzma`, `_decimal`, `readline`, `_curses`, `_tkinter` and mimalloc are
  disabled at configure time.
- Small C patches applied before transpiling are in internal/patch/ (each
  hunk carries an explanation).
- Functions of the C library that modernc.org/libc lacks on darwin are
  supplied in Go (libpython/libc_darwin.go); a few libc calls and compiler
  builtins are rewritten in the generated code (generator.go).
- The pure-Python standard library is embedded as a zip archive minus test
  suites, IDLE, tkinter, turtle and lib2to3.

## Software incorporated in CPython

The following notices are reproduced from CPython's Doc/license.rst for the
components that are part of this build.

### HACL* (md5, sha1, sha2, sha3, blake2, hmac)

MIT License

Copyright (c) 2016-2022 INRIA, CMU and Microsoft Corporation
Copyright (c) 2022-2023 HACL* Contributors

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.

### Mersenne Twister

The `!_random` C extension underlying the `random` module
includes code based on a download from
http://www.math.sci.hiroshima-u.ac.jp/~m-mat/MT/MT2002/emt19937ar.html. The following are
the verbatim comments from the original code::

   A C-program for MT19937, with initialization improved 2002/1/26.
   Coded by Takuji Nishimura and Makoto Matsumoto.

   Before using, initialize the state by using init_genrand(seed)
   or init_by_array(init_key, key_length).

   Copyright (C) 1997 - 2002, Makoto Matsumoto and Takuji Nishimura,
   All rights reserved.

   Redistribution and use in source and binary forms, with or without
   modification, are permitted provided that the following conditions
   are met:

    1. Redistributions of source code must retain the above copyright
       notice, this list of conditions and the following disclaimer.

    2. Redistributions in binary form must reproduce the above copyright
       notice, this list of conditions and the following disclaimer in the
       documentation and/or other materials provided with the distribution.

    3. The names of its contributors may not be used to endorse or promote
       products derived from this software without specific prior written
       permission.

   THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS
   "AS IS" AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT
   LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR
   A PARTICULAR PURPOSE ARE DISCLAIMED.  IN NO EVENT SHALL THE COPYRIGHT OWNER OR
   CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL,
   EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT LIMITED TO,
   PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE, DATA, OR
   PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY OF
   LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING
   NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE OF THIS
   SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.


   Any feedback is very welcome.
   http://www.math.sci.hiroshima-u.ac.jp/~m-mat/MT/emt.html
   email: m-mat @ math.sci.hiroshima-u.ac.jp (remove space)

### Cookie management

The `http.cookies` module contains the following notice::

   Copyright 2000 by Timothy O'Malley <timo@alum.mit.edu>

                  All Rights Reserved

   Permission to use, copy, modify, and distribute this software
   and its documentation for any purpose and without fee is hereby
   granted, provided that the above copyright notice appear in all
   copies and that both that copyright notice and this permission
   notice appear in supporting documentation, and that the name of
   Timothy O'Malley  not be used in advertising or publicity
   pertaining to distribution of the software without specific, written
   prior permission.

   Timothy O'Malley DISCLAIMS ALL WARRANTIES WITH REGARD TO THIS
   SOFTWARE, INCLUDING ALL IMPLIED WARRANTIES OF MERCHANTABILITY
   AND FITNESS, IN NO EVENT SHALL Timothy O'Malley BE LIABLE FOR
   ANY SPECIAL, INDIRECT OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES
   WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS,
   WHETHER IN AN ACTION OF CONTRACT, NEGLIGENCE OR OTHER TORTIOUS
   ACTION, ARISING OUT OF OR IN CONNECTION WITH THE USE OR
   PERFORMANCE OF THIS SOFTWARE.

### Execution tracing

The `trace` module contains the following notice::

   portions copyright 2001, Autonomous Zones Industries, Inc., all rights...
   err...  reserved and offered to the public under the terms of the
   Python 2.2 license.
   Author: Zooko O'Whielacronx
   http://zooko.com/
   mailto:zooko@zooko.com

   Copyright 2000, Mojam Media, Inc., all rights reserved.
   Author: Skip Montanaro

   Copyright 1999, Bioreason, Inc., all rights reserved.
   Author: Andrew Dalke

   Copyright 1995-1997, Automatrix, Inc., all rights reserved.
   Author: Skip Montanaro

   Copyright 1991-1995, Stichting Mathematisch Centrum, all rights reserved.


   Permission to use, copy, modify, and distribute this Python software and
   its associated documentation for any purpose without fee is hereby
   granted, provided that the above copyright notice appears in all copies,
   and that both that copyright notice and this permission notice appear in
   supporting documentation, and that the name of neither Automatrix,
   Bioreason or Mojam Media be used in advertising or publicity pertaining to
   distribution of the software without specific, written prior permission.

### XML Remote Procedure Calls

The `xmlrpc.client` module contains the following notice::

       The XML-RPC client interface is

   Copyright (c) 1999-2002 by Secret Labs AB
   Copyright (c) 1999-2002 by Fredrik Lundh

   By obtaining, using, and/or copying this software and/or its
   associated documentation, you agree that you have read, understood,
   and will comply with the following terms and conditions:

   Permission to use, copy, modify, and distribute this software and
   its associated documentation for any purpose and without fee is
   hereby granted, provided that the above copyright notice appears in
   all copies, and that both that copyright notice and this permission
   notice appear in supporting documentation, and that the name of
   Secret Labs AB or the author not be used in advertising or publicity
   pertaining to distribution of the software without specific, written
   prior permission.

   SECRET LABS AB AND THE AUTHOR DISCLAIMS ALL WARRANTIES WITH REGARD
   TO THIS SOFTWARE, INCLUDING ALL IMPLIED WARRANTIES OF MERCHANT-
   ABILITY AND FITNESS.  IN NO EVENT SHALL SECRET LABS AB OR THE AUTHOR
   BE LIABLE FOR ANY SPECIAL, INDIRECT OR CONSEQUENTIAL DAMAGES OR ANY
   DAMAGES WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS,
   WHETHER IN AN ACTION OF CONTRACT, NEGLIGENCE OR OTHER TORTIOUS
   ACTION, ARISING OUT OF OR IN CONNECTION WITH THE USE OR PERFORMANCE
   OF THIS SOFTWARE.

### SipHash24

The file `Python/pyhash.c` contains Marek Majkowski' implementation of
Dan Bernstein's SipHash24 algorithm. It contains the following note::

  <MIT License>
  Copyright (c) 2013  Marek Majkowski <marek@popcount.org>

  Permission is hereby granted, free of charge, to any person obtaining a copy
  of this software and associated documentation files (the "Software"), to deal
  in the Software without restriction, including without limitation the rights
  to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
  copies of the Software, and to permit persons to whom the Software is
  furnished to do so, subject to the following conditions:

  The above copyright notice and this permission notice shall be included in
  all copies or substantial portions of the Software.
  </MIT License>

  Original location:
     https://github.com/majek/csiphash/

  Solution inspired by code from:
     Samuel Neves (supercop/crypto_auth/siphash24/little)
     djb (supercop/crypto_auth/siphash24/little2)
     Jean-Philippe Aumasson (https://131002.net/siphash/siphash24.c)

### strtod and dtoa

The file `Python/dtoa.c`, which supplies C functions dtoa and
strtod for conversion of C doubles to and from strings, is derived
from the file of the same name by David M. Gay, currently available
from https://web.archive.org/web/20220517033456/http://www.netlib.org/fp/dtoa.c.
The original file, as retrieved on March 16, 2009, contains the following
copyright and licensing notice::

   /****************************************************************
    *
    * The author of this software is David M. Gay.
    *
    * Copyright (c) 1991, 2000, 2001 by Lucent Technologies.
    *
    * Permission to use, copy, modify, and distribute this software for any
    * purpose without fee is hereby granted, provided that this entire notice
    * is included in all copies of any software which is or includes a copy
    * or modification of this software and in all copies of the supporting
    * documentation for such software.
    *
    * THIS SOFTWARE IS BEING PROVIDED "AS IS", WITHOUT ANY EXPRESS OR IMPLIED
    * WARRANTY.  IN PARTICULAR, NEITHER THE AUTHOR NOR LUCENT MAKES ANY
    * REPRESENTATION OR WARRANTY OF ANY KIND CONCERNING THE MERCHANTABILITY
    * OF THIS SOFTWARE OR ITS FITNESS FOR ANY PARTICULAR PURPOSE.
    *
    ***************************************************************/

### expat

The `pyexpat <xml.parsers.expat>` extension is built using an included copy of the expat
sources unless the build is configured ``--with-system-expat``::

  Copyright (c) 1998, 1999, 2000 Thai Open Source Software Center Ltd
                                 and Clark Cooper

  Permission is hereby granted, free of charge, to any person obtaining
  a copy of this software and associated documentation files (the
  "Software"), to deal in the Software without restriction, including
  without limitation the rights to use, copy, modify, merge, publish,
  distribute, sublicense, and/or sell copies of the Software, and to
  permit persons to whom the Software is furnished to do so, subject to
  the following conditions:

  The above copyright notice and this permission notice shall be included
  in all copies or substantial portions of the Software.

  THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND,
  EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF
  MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT.
  IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY
  CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT,
  TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE
  SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.

### asyncio

Parts of the `asyncio` module are incorporated from
`uvloop 0.16 <https://github.com/MagicStack/uvloop/tree/v0.16.0>`_,
which is distributed under the MIT license::

  Copyright (c) 2015-2021 MagicStack Inc.  http://magic.io

  Permission is hereby granted, free of charge, to any person obtaining
  a copy of this software and associated documentation files (the
  "Software"), to deal in the Software without restriction, including
  without limitation the rights to use, copy, modify, merge, publish,
  distribute, sublicense, and/or sell copies of the Software, and to
  permit persons to whom the Software is furnished to do so, subject to
  the following conditions:

  The above copyright notice and this permission notice shall be
  included in all copies or substantial portions of the Software.

  THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND,
  EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF
  MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND
  NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE
  LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION
  OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION
  WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.

### Global Unbounded Sequences (GUS)

The file `Python/qsbr.c` is adapted from FreeBSD's "Global Unbounded
Sequences" safe memory reclamation scheme in
`subr_smr.c <https://github.com/freebsd/freebsd-src/blob/main/sys/kern/subr_smr.c>`_.
The file is distributed under the 2-Clause BSD License::

  Copyright (c) 2019,2020 Jeffrey Roberson <jeff@FreeBSD.org>

  Redistribution and use in source and binary forms, with or without
  modification, are permitted provided that the following conditions
  are met:
  1. Redistributions of source code must retain the above copyright
     notice unmodified, this list of conditions, and the following
     disclaimer.
  2. Redistributions in binary form must reproduce the above copyright
     notice, this list of conditions and the following disclaimer in the
     documentation and/or other materials provided with the distribution.

  THIS SOFTWARE IS PROVIDED BY THE AUTHOR "AS IS" AND ANY EXPRESS OR
  IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE IMPLIED WARRANTIES
  OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE DISCLAIMED.
  IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR ANY DIRECT, INDIRECT,
  INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT
  NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE,
  DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY
  THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT
  (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE OF
  THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.

### Unicode Character Database

An extract of the Unicode Character Database,
converted to an internal format, is used by the `unicodedata` module and
for the Unicode support of the `str` type. The original Unicode data
files are distributed under the Unicode License::

  UNICODE LICENSE V3

  COPYRIGHT AND PERMISSION NOTICE

  Copyright © 1991-2026 Unicode, Inc.

  NOTICE TO USER: Carefully read the following legal agreement. BY
  DOWNLOADING, INSTALLING, COPYING OR OTHERWISE USING DATA FILES, AND/OR
  SOFTWARE, YOU UNEQUIVOCALLY ACCEPT, AND AGREE TO BE BOUND BY, ALL OF THE
  TERMS AND CONDITIONS OF THIS AGREEMENT. IF YOU DO NOT AGREE, DO NOT
  DOWNLOAD, INSTALL, COPY, DISTRIBUTE OR USE THE DATA FILES OR SOFTWARE.

  Permission is hereby granted, free of charge, to any person obtaining a
  copy of data files and any associated documentation (the "Data Files") or
  software and any associated documentation (the "Software") to deal in the
  Data Files or Software without restriction, including without limitation
  the rights to use, copy, modify, merge, publish, distribute, and/or sell
  copies of the Data Files or Software, and to permit persons to whom the
  Data Files or Software are furnished to do so, provided that either (a)
  this copyright and permission notice appear with all copies of the Data
  Files or Software, or (b) this copyright and permission notice appear in
  associated Documentation.

  THE DATA FILES AND SOFTWARE ARE PROVIDED "AS IS", WITHOUT WARRANTY OF ANY
  KIND, EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF
  MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT OF
  THIRD PARTY RIGHTS.

  IN NO EVENT SHALL THE COPYRIGHT HOLDER OR HOLDERS INCLUDED IN THIS NOTICE
  BE LIABLE FOR ANY CLAIM, OR ANY SPECIAL INDIRECT OR CONSEQUENTIAL DAMAGES,
  OR ANY DAMAGES WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS,
  WHETHER IN AN ACTION OF CONTRACT, NEGLIGENCE OR OTHER TORTIOUS ACTION,
  ARISING OUT OF OR IN CONNECTION WITH THE USE OR PERFORMANCE OF THE DATA
  FILES OR SOFTWARE.

  Except as contained in this notice, the name of a copyright holder shall
  not be used in advertising or otherwise to promote the sale, use or other
  dealings in these Data Files or Software without prior written
  authorization of the copyright holder.
