package weed_server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/chrislusf/seaweedfs/weed/filer2"
	"github.com/chrislusf/seaweedfs/weed/glog"
	"github.com/chrislusf/seaweedfs/weed/operation"
	"github.com/chrislusf/seaweedfs/weed/pb/filer_pb"
	"github.com/chrislusf/seaweedfs/weed/util"
	"strconv"
	"strings"
)

func (fs *FilerServer) LookupDirectoryEntry(ctx context.Context, req *filer_pb.LookupDirectoryEntryRequest) (*filer_pb.LookupDirectoryEntryResponse, error) {

	entry, err := fs.filer.FindEntry(filer2.FullPath(filepath.Join(req.Directory, req.Name)))
	if err != nil {
		return nil, fmt.Errorf("%s not found under %s: %v", req.Name, req.Directory, err)
	}

	return &filer_pb.LookupDirectoryEntryResponse{
		Entry: &filer_pb.Entry{
			Name:        req.Name,
			IsDirectory: entry.IsDirectory(),
			Attributes:  filer2.EntryAttributeToPb(entry),
			Chunks:      entry.Chunks,
		},
	}, nil
}

func (fs *FilerServer) ListEntries(ctx context.Context, req *filer_pb.ListEntriesRequest) (*filer_pb.ListEntriesResponse, error) {

	limit := int(req.Limit)
	if limit == 0 {
		limit = fs.option.DirListingLimit
	}

	resp := &filer_pb.ListEntriesResponse{}
	lastFileName := req.StartFromFileName
	includeLastFile := req.InclusiveStartFrom
	for limit > 0 {
		entries, err := fs.filer.ListDirectoryEntries(filer2.FullPath(req.Directory), lastFileName, includeLastFile, limit)
		if err != nil {
			return nil, err
		}
		if len(entries) == 0 {
			return resp, nil
		}

		includeLastFile = false

		for _, entry := range entries {

			lastFileName = entry.Name()

			if req.Prefix != "" {
				if !strings.HasPrefix(entry.Name(), req.Prefix) {
					continue
				}
			}

			resp.Entries = append(resp.Entries, &filer_pb.Entry{
				Name:        entry.Name(),
				IsDirectory: entry.IsDirectory(),
				Chunks:      entry.Chunks,
				Attributes:  filer2.EntryAttributeToPb(entry),
			})
			limit--
		}

	}

	return resp, nil
}

func (fs *FilerServer) GetEntryAttributes(ctx context.Context, req *filer_pb.GetEntryAttributesRequest) (*filer_pb.GetEntryAttributesResponse, error) {

	fullpath := filer2.NewFullPath(req.ParentDir, req.Name)

	entry, err := fs.filer.FindEntry(fullpath)
	if err != nil {
		return nil, fmt.Errorf("FindEntry %s: %v", fullpath, err)
	}

	attributes := filer2.EntryAttributeToPb(entry)

	glog.V(3).Infof("GetEntryAttributes %v size %d chunks %d: %+v", fullpath, attributes.FileSize, len(entry.Chunks), attributes)

	return &filer_pb.GetEntryAttributesResponse{
		Attributes: attributes,
		Chunks:     entry.Chunks,
	}, nil
}

func (fs *FilerServer) LookupVolume(ctx context.Context, req *filer_pb.LookupVolumeRequest) (*filer_pb.LookupVolumeResponse, error) {

	resp := &filer_pb.LookupVolumeResponse{
		LocationsMap: make(map[string]*filer_pb.Locations),
	}

	for _, vidString := range req.VolumeIds {
		vid, err := strconv.Atoi(vidString)
		if err != nil {
			glog.V(1).Infof("Unknown volume id %d", vid)
			return nil, err
		}
		var locs []*filer_pb.Location
		for _, loc := range fs.filer.MasterClient.GetLocations(uint32(vid)) {
			locs = append(locs, &filer_pb.Location{
				Url:       loc.Url,
				PublicUrl: loc.PublicUrl,
			})
		}
		resp.LocationsMap[vidString] = &filer_pb.Locations{
			Locations: locs,
		}
	}

	return resp, nil
}

func (fs *FilerServer) CreateEntry(ctx context.Context, req *filer_pb.CreateEntryRequest) (resp *filer_pb.CreateEntryResponse, err error) {

	fullpath := filer2.FullPath(filepath.Join(req.Directory, req.Entry.Name))
	chunks, garbages := filer2.CompactFileChunks(req.Entry.Chunks)

	for _, garbage := range garbages {
		glog.V(0).Infof("deleting %s garbage chunk: %v, [%d, %d)", fullpath, garbage.FileId, garbage.Offset, garbage.Offset+int64(garbage.Size))
		fs.filer.DeleteFileByFileId(garbage.FileId)
	}

	err = fs.filer.CreateEntry(&filer2.Entry{
		FullPath: fullpath,
		Attr:     filer2.PbToEntryAttribute(req.Entry.Attributes),
		Chunks:   chunks,
	})

	if err == nil {
	}

	return &filer_pb.CreateEntryResponse{}, err
}

func (fs *FilerServer) UpdateEntry(ctx context.Context, req *filer_pb.UpdateEntryRequest) (*filer_pb.UpdateEntryResponse, error) {

	fullpath := filepath.Join(req.Directory, req.Entry.Name)
	entry, err := fs.filer.FindEntry(filer2.FullPath(fullpath))
	if err != nil {
		return &filer_pb.UpdateEntryResponse{}, fmt.Errorf("not found %s: %v", fullpath, err)
	}

	// remove old chunks if not included in the new ones
	unusedChunks := filer2.FindUnusedFileChunks(entry.Chunks, req.Entry.Chunks)

	chunks, garbages := filer2.CompactFileChunks(req.Entry.Chunks)

	newEntry := &filer2.Entry{
		FullPath: filer2.FullPath(filepath.Join(req.Directory, req.Entry.Name)),
		Attr:     entry.Attr,
		Chunks:   chunks,
	}

	glog.V(3).Infof("updating %s: %+v, chunks %d: %v => %+v, chunks %d: %v",
		fullpath, entry.Attr, len(entry.Chunks), entry.Chunks,
		req.Entry.Attributes, len(req.Entry.Chunks), req.Entry.Chunks)

	if req.Entry.Attributes != nil {
		if req.Entry.Attributes.Mtime != 0 {
			newEntry.Attr.Mtime = time.Unix(req.Entry.Attributes.Mtime, 0)
		}
		if req.Entry.Attributes.FileMode != 0 {
			newEntry.Attr.Mode = os.FileMode(req.Entry.Attributes.FileMode)
		}
		newEntry.Attr.Uid = req.Entry.Attributes.Uid
		newEntry.Attr.Gid = req.Entry.Attributes.Gid
		newEntry.Attr.Mime = req.Entry.Attributes.Mime

	}

	if filer2.EqualEntry(entry, newEntry) {
		return &filer_pb.UpdateEntryResponse{}, err
	}

	if err = fs.filer.UpdateEntry(newEntry); err == nil {
		for _, garbage := range unusedChunks {
			glog.V(0).Infof("deleting %s old chunk: %v, [%d, %d)", fullpath, garbage.FileId, garbage.Offset, garbage.Offset+int64(garbage.Size))
			fs.filer.DeleteFileByFileId(garbage.FileId)
		}
		for _, garbage := range garbages {
			glog.V(0).Infof("deleting %s garbage chunk: %v, [%d, %d)", fullpath, garbage.FileId, garbage.Offset, garbage.Offset+int64(garbage.Size))
			fs.filer.DeleteFileByFileId(garbage.FileId)
		}
	}

	fs.filer.NotifyUpdateEvent(entry, newEntry, true)

	return &filer_pb.UpdateEntryResponse{}, err
}

func (fs *FilerServer) DeleteEntry(ctx context.Context, req *filer_pb.DeleteEntryRequest) (resp *filer_pb.DeleteEntryResponse, err error) {
	err = fs.filer.DeleteEntryMetaAndData(filer2.FullPath(filepath.Join(req.Directory, req.Name)), req.IsRecursive, req.IsDeleteData)
	return &filer_pb.DeleteEntryResponse{}, err
}

func (fs *FilerServer) AssignVolume(ctx context.Context, req *filer_pb.AssignVolumeRequest) (resp *filer_pb.AssignVolumeResponse, err error) {

	ttlStr := ""
	if req.TtlSec > 0 {
		ttlStr = strconv.Itoa(int(req.TtlSec))
	}

	var altRequest *operation.VolumeAssignRequest

	dataCenter := req.DataCenter
	if dataCenter == "" {
		dataCenter = fs.option.DataCenter
	}

	assignRequest := &operation.VolumeAssignRequest{
		Count:       uint64(req.Count),
		Replication: req.Replication,
		Collection:  req.Collection,
		Ttl:         ttlStr,
		DataCenter:  dataCenter,
	}
	if dataCenter != "" {
		altRequest = &operation.VolumeAssignRequest{
			Count:       uint64(req.Count),
			Replication: req.Replication,
			Collection:  req.Collection,
			Ttl:         ttlStr,
			DataCenter:  "",
		}
	}
	assignResult, err := operation.Assign(fs.filer.GetMaster(), assignRequest, altRequest)
	if err != nil {
		return nil, fmt.Errorf("assign volume: %v", err)
	}
	if assignResult.Error != "" {
		return nil, fmt.Errorf("assign volume result: %v", assignResult.Error)
	}

	return &filer_pb.AssignVolumeResponse{
		FileId:    assignResult.Fid,
		Count:     int32(assignResult.Count),
		Url:       assignResult.Url,
		PublicUrl: assignResult.PublicUrl,
	}, err
}

func (fs *FilerServer) DeleteCollection(ctx context.Context, req *filer_pb.DeleteCollectionRequest) (resp *filer_pb.DeleteCollectionResponse, err error) {

	for _, master := range fs.option.Masters {
		_, err = util.Get(fmt.Sprintf("http://%s/col/delete?collection=%s", master, req.Collection))
	}

	return &filer_pb.DeleteCollectionResponse{}, err
}
