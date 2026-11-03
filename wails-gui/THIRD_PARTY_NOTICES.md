# Third-party notices

Yansu bundles the following software in the desktop app.

## magpie

Yansu bundles the `magpie` command-line build (https://github.com/yetone/magpie) and runs it as a local model gateway. magpie is distributed under the MIT License:

Copyright (c) yetone

Permission is hereby granted, free of charge, to any person obtaining a copy of this software and associated documentation files (the "Software"), to deal in the Software without restriction, including without limitation the rights to use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of the Software, and to permit persons to whom the Software is furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.

The pinned version and checksums are in `scripts/download-magpie.sh`. Bun, FFmpeg, Git and the CuaDriver bundles carry their own licenses in their download scripts and bundle directories.

## Call speaker models

The existing sherpa-onnx runtime (Apache-2.0) runs the following locally installed
models. Versions and SHA-256 pins are recorded in `../docs/call-turns.md` and
`bundled_sherpa.go`.

3D-Speaker CAM++ (`3dspeaker_speech_campplus_sv_zh-cn_16k-common.onnx`) is from
[3D-Speaker](https://github.com/modelscope/3D-Speaker), copyright Alibaba Group,
licensed under [Apache License 2.0](https://www.apache.org/licenses/LICENSE-2.0).

pyannote segmentation 3.0 int8 is from the pinned
[sherpa export](https://huggingface.co/csukuangfj/sherpa-onnx-pyannote-segmentation-3-0/tree/9403a6902bb58e3d5ae8c7e77c3422de279db2e0),
under the MIT License:

Copyright (c) 2022 CNRS

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
