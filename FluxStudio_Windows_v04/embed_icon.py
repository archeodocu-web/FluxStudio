#!/usr/bin/env python3
"""Embed an ICO as Win32 RT_GROUP_ICON/RT_ICON resources in a PE executable."""
import argparse, os, struct


def align(x, a):
    return (x + a - 1) // a * a


def parse_ico(path):
    data = open(path, 'rb').read()
    reserved, typ, count = struct.unpack_from('<HHH', data, 0)
    if reserved != 0 or typ != 1 or count == 0:
        raise ValueError('Invalid ICO file')
    images = []
    group = bytearray(struct.pack('<HHH', 0, 1, count))
    for i in range(count):
        off = 6 + i * 16
        width, height, colors, reserved, planes, bpp, size, image_off = struct.unpack_from('<BBBBHHII', data, off)
        raw = data[image_off:image_off+size]
        if len(raw) != size:
            raise ValueError('Truncated ICO image data')
        rid = i + 1
        images.append((rid, raw))
        group += struct.pack('<BBBBHHIH', width, height, colors, reserved, planes, bpp, size, rid)
    return images, bytes(group)


def build_resource_section(images, group, base_rva):
    # Directory nodes use identifiers (all numeric, sorted ascending).
    dirs = []
    root = {'id': None, 'entries': []}
    dirs.append(root)
    icon_type = {'id': 3, 'entries': []}
    group_type = {'id': 14, 'entries': []}
    root['entries'] = [(3, icon_type), (14, group_type)]
    datas = []
    for rid, raw in images:
        name_dir = {'id': rid, 'entries': [(0, {'raw': raw})]}
        dirs.append(name_dir)
        icon_type['entries'].append((rid, name_dir))
    group_name = {'id': 1, 'entries': [(0, {'raw': group})]}
    group_type['entries'].append((1, group_name))
    # Keep tree node order deterministic and place type nodes before names.
    dirs = [root, icon_type, group_type] + [d for d in dirs if d not in (root, icon_type, group_type)] + [group_name]
    # Allocate directories.
    cursor = 0
    for d in dirs:
        d['offset'] = cursor
        cursor += 16 + 8 * len(d['entries'])
    # Allocate data-entry records (one for every language leaf).
    data_records = []
    for d in dirs:
        for ident, target in d['entries']:
            if isinstance(target, dict) and 'raw' in target:
                target['record_offset'] = cursor
                data_records.append(target)
                cursor += 16
    # Allocate raw payloads, 4-byte aligned.
    payload_start = align(cursor, 4)
    cursor = payload_start
    for rec in data_records:
        cursor = align(cursor, 4)
        rec['raw_offset'] = cursor
        cursor += len(rec['raw'])
    blob = bytearray(cursor)
    # Write directory headers and entries.
    for d in dirs:
        entries = sorted(d['entries'], key=lambda x: x[0])
        # characteristics, timestamp, major/minor, named count, id count
        struct.pack_into('<IIHHHH', blob, d['offset'], 0, 0, 0, 0, 0, len(entries))
        eoff = d['offset'] + 16
        for i, (ident, target) in enumerate(entries):
            if isinstance(target, dict) and 'offset' in target:
                target_offset = 0x80000000 | target['offset']
            else:
                target_offset = target['record_offset']
            struct.pack_into('<II', blob, eoff + 8*i, ident, target_offset)
    # Write data entry and payload records.
    for rec in data_records:
        raw, data_record_off, raw_off = rec['raw'], rec['record_offset'], rec['raw_offset']
        struct.pack_into('<IIII', blob, data_record_off, base_rva + raw_off, len(raw), 0, 0)
        blob[raw_off:raw_off+len(raw)] = raw
    return bytes(blob)


def inject(exe_path, ico_path, out_path):
    pe = bytearray(open(exe_path, 'rb').read())
    if pe[:2] != b'MZ': raise ValueError('Not an MZ executable')
    peoff = struct.unpack_from('<I', pe, 0x3c)[0]
    if pe[peoff:peoff+4] != b'PE\0\0': raise ValueError('Invalid PE signature')
    coff = peoff + 4
    machine, nsec, ts, ptrsym, numsym, opt_size, chars = struct.unpack_from('<HHIIIHH', pe, coff)
    opt = coff + 20
    magic = struct.unpack_from('<H', pe, opt)[0]
    if magic != 0x20b: raise ValueError('Expected PE32+ executable')
    section_alignment = struct.unpack_from('<I', pe, opt+32)[0]
    file_alignment = struct.unpack_from('<I', pe, opt+36)[0]
    size_of_image_off = opt+56
    size_of_headers = struct.unpack_from('<I', pe, opt+60)[0]
    data_dir = opt+112
    resource_dir = data_dir + 2*8
    sec_table = opt + opt_size
    if nsec >= 96: raise ValueError('Too many PE sections')
    # Read all existing sections and determine a safe RVA for the new section.
    max_rva_end = 0
    for i in range(nsec):
        sh = sec_table + i*40
        vsize, va, rawsize, rawptr = struct.unpack_from('<IIII', pe, sh+8)
        max_rva_end = max(max_rva_end, va + max(vsize, rawsize))
    new_rva = align(max_rva_end, section_alignment)
    raw_ptr = align(len(pe), file_alignment)
    images, group = parse_ico(ico_path)
    blob = build_resource_section(images, group, new_rva)
    raw_size = align(len(blob), file_alignment)
    blob_padded = blob + b'\0'*(raw_size-len(blob))
    # The new section header must fit inside existing SizeOfHeaders.
    new_sh = sec_table + nsec*40
    if new_sh + 40 > size_of_headers:
        raise ValueError('No room for another section header in PE headers')
    pe.extend(b'\0'*(raw_ptr-len(pe)))
    pe.extend(blob_padded)
    # Add .rsrc section.
    pe[new_sh:new_sh+40] = b'\0'*40
    pe[new_sh:new_sh+8] = b'.rsrc\0\0\0'
    struct.pack_into('<IIII', pe, new_sh+8, len(blob), new_rva, raw_size, raw_ptr)
    struct.pack_into('<II', pe, new_sh+24, 0, 0)  # reloc and line number ptr
    struct.pack_into('<HHI', pe, new_sh+32, 0, 0, 0x40000040)  # initialized/read-only
    # Update section count, resources data directory, SizeOfImage.
    struct.pack_into('<H', pe, coff+2, nsec+1)
    struct.pack_into('<II', pe, resource_dir, new_rva, len(blob))
    struct.pack_into('<I', pe, size_of_image_off, align(new_rva+len(blob), section_alignment))
    open(out_path, 'wb').write(pe)

if __name__ == '__main__':
    ap = argparse.ArgumentParser()
    ap.add_argument('exe')
    ap.add_argument('ico')
    ap.add_argument('out')
    a = ap.parse_args()
    inject(a.exe, a.ico, a.out)
