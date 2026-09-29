// nlcvectors: a small CLI harness around the vendored GroovyNLC reference
// codec (nlc_codec.h/.cpp), used to generate synthetic test images and to
// run them through nlc_encode/nlc_decode so the results can be captured as
// golden test vectors for the pure-Go port in internal/groovy/nlc.
//
// Two modes:
//   nlcvectors gen <kind> <w> <h> <out.rgb>
//   nlcvectors run <in.rgb> <w> <h> <near> <tiled|rice> <out.nlc> <out.decoded>
//
// See tools/nlcvectors/README.md and tools/nlcvectors/gen.sh for how this
// is driven.

#include "nlc_codec.h"

#include <cstdint>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <string>
#include <vector>

namespace {

bool writeFile(const std::string& path, const uint8_t* data, size_t n) {
    FILE* f = std::fopen(path.c_str(), "wb");
    if (!f) {
        std::fprintf(stderr, "nlcvectors: cannot open %s for write\n", path.c_str());
        return false;
    }
    size_t written = std::fwrite(data, 1, n, f);
    std::fclose(f);
    if (written != n) {
        std::fprintf(stderr, "nlcvectors: short write to %s (%zu of %zu bytes)\n",
                      path.c_str(), written, n);
        return false;
    }
    return true;
}

bool readFile(const std::string& path, std::vector<uint8_t>* out) {
    FILE* f = std::fopen(path.c_str(), "rb");
    if (!f) {
        std::fprintf(stderr, "nlcvectors: cannot open %s for read\n", path.c_str());
        return false;
    }
    std::fseek(f, 0, SEEK_END);
    long sz = std::ftell(f);
    if (sz < 0) {
        std::fclose(f);
        std::fprintf(stderr, "nlcvectors: cannot stat %s\n", path.c_str());
        return false;
    }
    std::fseek(f, 0, SEEK_SET);
    out->resize((size_t)sz);
    size_t got = sz > 0 ? std::fread(out->data(), 1, (size_t)sz, f) : 0;
    std::fclose(f);
    if (got != (size_t)sz) {
        std::fprintf(stderr, "nlcvectors: short read from %s (%zu of %ld bytes)\n",
                      path.c_str(), got, sz);
        return false;
    }
    return true;
}

// ---------------------------------------------------------------------------
// gen: deterministic synthetic RGB888 image generators
// ---------------------------------------------------------------------------

int cmdGen(int argc, char** argv) {
    if (argc != 6) {
        std::fprintf(stderr,
                      "usage: nlcvectors gen <kind> <w> <h> <out.rgb>\n"
                      "  kind in {flat,gradient,edges,noise,primaries,spikes}\n");
        return 1;
    }
    std::string kind = argv[2];
    int w = std::atoi(argv[3]);
    int h = std::atoi(argv[4]);
    std::string outPath = argv[5];
    if (w <= 0 || h <= 0) {
        std::fprintf(stderr, "nlcvectors: bad width/height %d x %d\n", w, h);
        return 1;
    }

    std::vector<uint8_t> img((size_t)w * (size_t)h * 3);

    if (kind == "flat") {
        for (size_t i = 0; i < img.size(); i += 3) {
            img[i + 0] = 64;
            img[i + 1] = 200;
            img[i + 2] = 90;
        }
    } else if (kind == "gradient") {
        for (int y = 0; y < h; y++) {
            for (int x = 0; x < w; x++) {
                size_t o = ((size_t)y * w + x) * 3;
                int r = (w > 1) ? (x * 255 / (w - 1)) : 0;
                int g = (h > 1) ? (y * 255 / (h - 1)) : 0;
                int b = (x + y) & 255;
                img[o + 0] = (uint8_t)r;
                img[o + 1] = (uint8_t)g;
                img[o + 2] = (uint8_t)b;
            }
        }
    } else if (kind == "edges") {
        for (int y = 0; y < h; y++) {
            for (int x = 0; x < w; x++) {
                size_t o = ((size_t)y * w + x) * 3;
                bool white = ((x / 11) % 2) == 0;
                uint8_t v = white ? 255 : 0;
                img[o + 0] = v;
                img[o + 1] = v;
                img[o + 2] = v;
            }
        }
        // Overlay a 1-px diagonal line of (255, 0, 128).
        int n = w < h ? w : h;
        for (int i = 0; i < n; i++) {
            size_t o = ((size_t)i * w + i) * 3;
            img[o + 0] = 255;
            img[o + 1] = 0;
            img[o + 2] = 128;
        }
    } else if (kind == "noise") {
        uint32_t s = 12345u;
        for (size_t i = 0; i < img.size(); i++) {
            s = s * 1664525u + 1013904223u;
            img[i] = (uint8_t)(s >> 24);
        }
    } else if (kind == "primaries") {
        // 4px checkerboard cycling through pure red, green, blue, cyan,
        // magenta, yellow.
        static const uint8_t colors[6][3] = {
            {255, 0, 0}, {0, 255, 0}, {0, 0, 255},
            {0, 255, 255}, {255, 0, 255}, {255, 255, 0},
        };
        for (int y = 0; y < h; y++) {
            for (int x = 0; x < w; x++) {
                size_t o = ((size_t)y * w + x) * 3;
                int cellX = x / 4;
                int cellY = y / 4;
                int idx = (cellX + cellY) % 6;
                img[o + 0] = colors[idx][0];
                img[o + 1] = colors[idx][1];
                img[o + 2] = colors[idx][2];
            }
        }
    } else if (kind == "spikes") {
        for (size_t i = 0; i < img.size(); i += 3) {
            img[i + 0] = 40;
            img[i + 1] = 40;
            img[i + 2] = 40;
        }
        size_t npix = (size_t)w * (size_t)h;
        for (size_t p = 0; p < npix; p++) {
            size_t o = p * 3;
            if (p % 37 == 0) {
                img[o + 0] = 255;
                img[o + 1] = 255;
                img[o + 2] = 255;
            }
            if (p % 53 == 0) {
                img[o + 0] = 0;
                img[o + 1] = 0;
                img[o + 2] = 0;
            }
        }
    } else {
        std::fprintf(stderr, "nlcvectors: unknown kind '%s'\n", kind.c_str());
        return 1;
    }

    if (!writeFile(outPath, img.data(), img.size())) return 1;
    return 0;
}

// ---------------------------------------------------------------------------
// run: encode + decode one image through the reference codec
// ---------------------------------------------------------------------------

int cmdRun(int argc, char** argv) {
    if (argc != 9) {
        std::fprintf(stderr,
                      "usage: nlcvectors run <in.rgb> <w> <h> <near> <tiled|rice> "
                      "<out.nlc> <out.decoded>\n");
        return 1;
    }
    std::string inPath = argv[2];
    int w = std::atoi(argv[3]);
    int h = std::atoi(argv[4]);
    int near = std::atoi(argv[5]);
    std::string packStr = argv[6];
    std::string outNlcPath = argv[7];
    std::string outDecodedPath = argv[8];

    if (w <= 0 || h <= 0) {
        std::fprintf(stderr, "nlcvectors: bad width/height %d x %d\n", w, h);
        return 1;
    }

    nlc_pack_t pack;
    if (packStr == "tiled") {
        pack = NLC_PACK_TILED;
    } else if (packStr == "rice") {
        pack = NLC_PACK_RICE;
    } else {
        std::fprintf(stderr, "nlcvectors: unknown pack '%s' (want tiled|rice)\n", packStr.c_str());
        return 1;
    }

    std::vector<uint8_t> src;
    if (!readFile(inPath, &src)) return 1;

    size_t expected = (size_t)w * (size_t)h * 3;
    if (src.size() != expected) {
        std::fprintf(stderr, "nlcvectors: %s has %zu bytes, expected %zu for %dx%d RGB888\n",
                      inPath.c_str(), src.size(), expected, w, h);
        return 1;
    }

    // Built exactly like GroovyMister::buildNlcParams (api/groovymister.cpp).
    nlc_params np;
    std::memset(&np, 0, sizeof(np));
    np.width = w;
    np.height = h;
    np.rgb = NLC_RGB888;
    np.color = NLC_COLOR_YCOCG;
    np.near_lvl = near;
    np.pack = pack;
    np.tile = 16;
    np.width_bits = 4;
    np.rice_k = -1;

    size_t cap = nlc_max_encoded_size(&np);
    std::vector<uint8_t> dst(cap);
    int csize = nlc_encode(src.data(), dst.data(), cap, &np);
    if (csize < 0) {
        std::fprintf(stderr, "nlcvectors: nlc_encode failed for %s (near=%d pack=%s)\n",
                      inPath.c_str(), near, packStr.c_str());
        return 1;
    }
    if (!writeFile(outNlcPath, dst.data(), (size_t)csize)) return 1;

    std::vector<uint8_t> decoded(expected);
    int rc = nlc_decode(dst.data(), (size_t)csize, decoded.data(), &np);
    if (rc != 0) {
        std::fprintf(stderr, "nlcvectors: nlc_decode failed for %s (near=%d pack=%s)\n",
                      inPath.c_str(), near, packStr.c_str());
        return 1;
    }
    if (!writeFile(outDecodedPath, decoded.data(), decoded.size())) return 1;

    if (near == 0) {
        if (decoded != src) {
            std::fprintf(stderr,
                          "nlcvectors: near=0 but decoded != input for %s (pack=%s)\n",
                          inPath.c_str(), packStr.c_str());
            return 1;
        }
    }

    return 0;
}

}  // namespace

int main(int argc, char** argv) {
    if (argc < 2) {
        std::fprintf(stderr,
                      "usage: nlcvectors gen <kind> <w> <h> <out.rgb>\n"
                      "       nlcvectors run <in.rgb> <w> <h> <near> <tiled|rice> "
                      "<out.nlc> <out.decoded>\n");
        return 1;
    }
    std::string mode = argv[1];
    if (mode == "gen") return cmdGen(argc, argv);
    if (mode == "run") return cmdRun(argc, argv);
    std::fprintf(stderr, "nlcvectors: unknown mode '%s'\n", mode.c_str());
    return 1;
}
