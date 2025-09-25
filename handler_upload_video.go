package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

type FFProbeVideoInfo struct {
	Programs     []interface{} `json:"programs"`
	StreamGroups []interface{} `json:"stream_groups"`
	Streams      []struct {
		Width              int    `json:"width,omitempty"`
		Height             int    `json:"height,omitempty"`
		DisplayAspectRatio string `json:"display_aspect_ratio,omitempty"`
		Disposition        struct {
			Default         int `json:"default"`
			Dub             int `json:"dub"`
			Original        int `json:"original"`
			Comment         int `json:"comment"`
			Lyrics          int `json:"lyrics"`
			Karaoke         int `json:"karaoke"`
			Forced          int `json:"forced"`
			HearingImpaired int `json:"hearing_impaired"`
			VisualImpaired  int `json:"visual_impaired"`
			CleanEffects    int `json:"clean_effects"`
			AttachedPic     int `json:"attached_pic"`
			TimedThumbnails int `json:"timed_thumbnails"`
			NonDiegetic     int `json:"non_diegetic"`
			Captions        int `json:"captions"`
			Descriptions    int `json:"descriptions"`
			Metadata        int `json:"metadata"`
			Dependent       int `json:"dependent"`
			StillImage      int `json:"still_image"`
			Multilayer      int `json:"multilayer"`
		} `json:"disposition"`
		Tags struct {
			Language    string `json:"language"`
			HandlerName string `json:"handler_name"`
			VendorID    string `json:"vendor_id"`
			Encoder     string `json:"encoder"`
			Timecode    string `json:"timecode"`
		} `json:"tags"`
	} `json:"streams"`
}

func processVideoForFastStart(filePath string) (string, error) {
	// use ffmpeg to process the video for fast start
	exec.Command("ffmpeg").Run() // ensure ffmpeg is installed
	outputFilePath := strings.TrimSuffix(filePath, ".mp4") + "-faststart.mp4"
	cmd := exec.Command("ffmpeg", "-i", filePath, "-c", "copy", "-movflags", "faststart", "-f", "mp4", outputFilePath)
	err := cmd.Run()
	if err != nil {
		return "", err
	}
	return outputFilePath, nil
}

func getVideoAspectRatio(filePath string) (string, error) {
	// use ffprobe to get the video aspect ratio
	exec.Command("ffprobe").Run() // ensure ffprobe is installed
	// ffprobe -v error -show_streams samples/boots-video-horizontal.mp4 -show_entries stream=display_aspect_ratio,width,height -print_format json
	cmd := exec.Command("ffprobe", "-v", "error", "-show_streams", filePath, "-show_entries", "stream=display_aspect_ratio", "-print_format", "json")
	output, err := cmd.Output()

	if err != nil {
		return "", err
	}

	var info FFProbeVideoInfo
	err = json.Unmarshal(output, &info)
	if err != nil {
		return "", err
	}

	if len(info.Streams) == 0 {
		return "", fmt.Errorf("no streams found")
	}

	return info.Streams[0].DisplayAspectRatio, nil
}

func getVideoExtension(s string) (string, error) {
	mediaType, _, err := mime.ParseMediaType(s)
	if err != nil {
		return "", err
	}
	items := strings.Split(mediaType, "/")
	if len(items) < 2 || len(items) > 2 {
		return "", fmt.Errorf("not a valid video mime-type")
	}
	kind, extension := items[0], items[1]
	if kind == "video" {
		return extension, nil
	}
	return "", fmt.Errorf("not an video")
}

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return
	}

	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}

	video, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusNotFound, "Video not found", err)
		return
	}

	user, err := cfg.db.GetUser(userID)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "User not found", err)
		return
	}

	if video.UserID != user.ID {
		respondWithError(w, http.StatusUnauthorized, "Unauthorized", err)
		return
	}

	fmt.Println("uploading thumbnail for video", videoID, "by user", userID)

	const maxMemory = 10 << 30
	http.MaxBytesReader(w, r.Body, maxMemory)

	file, header, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error processing request", err)
		return

	}
	defer file.Close()
	contentType := header.Header.Get("Content-Type") // just assume that even an empty mime-type is valid
	extension, err := getVideoExtension(contentType)

	if err != nil {
		respondWithError(w, http.StatusNotAcceptable, "Not acceptable", err)
		return

	}

	videoFilename := fmt.Sprintf("%s.%s", video.ID.String(), extension)
	videoFile, err := os.CreateTemp("", videoFilename)

	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Server error", err)
		return
	}
	defer videoFile.Close()
	defer os.Remove(videoFile.Name())

	_, err = io.Copy(videoFile, file)

	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Server error", err)
		return
	}

	// process the video for fast start
	fastStartVideoFilePath, err := processVideoForFastStart(videoFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error processing video for fast start", err)
		return
	}

	fastStartedVideoFile, err := os.Open(fastStartVideoFilePath)

	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error opening video file with fast start version", err)
		return
	}

	defer fastStartedVideoFile.Close()
	defer os.Remove(fastStartVideoFilePath)

	// determine the aspect ratio of the video
	aspectRatio, err := getVideoAspectRatio(fastStartedVideoFile.Name())

	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error determining aspect ratio", err)
		return
	}

	mappingOfAspectRatios := map[string]string{
		"16:9":  "landscape",
		"9:16":  "portrait",
		"other": "other",
	}

	namedAspectRatio, ok := mappingOfAspectRatios[aspectRatio]

	if !ok {
		namedAspectRatio = "other"
	}
	_, err = videoFile.Seek(0, 0)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Server error", err)
		return
	}

	numBytes := 32
	randomBytes := make([]byte, numBytes)

	// Read random bytes into the slice
	_, err = rand.Read(randomBytes)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error generating random bytes", err)
		return
	}

	// Encode the random bytes into a hexadecimal string
	hexString := hex.EncodeToString(randomBytes)

	keyFilename := fmt.Sprintf("%s/%s.%s", namedAspectRatio, hexString, extension)

	// upload to S3
	_, err = cfg.s3Client.PutObject(r.Context(), &s3.PutObjectInput{
		Bucket:      &cfg.s3Bucket,
		Key:         aws.String(keyFilename),
		Body:        fastStartedVideoFile,
		ContentType: &contentType,
	})

	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error uploading to S3", err)
		return
	}

	scheme := "https"

	videoUrl := fmt.Sprintf("%s://%s.s3.%s.amazonaws.com/%s", scheme, cfg.s3Bucket, cfg.s3Region, keyFilename)
	video.UpdatedAt = time.Now()
	video.VideoURL = &videoUrl

	err = cfg.db.UpdateVideo(video)

	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error updating data", err)
		return

	}

	respondWithJSON(w, http.StatusOK, video)
}
