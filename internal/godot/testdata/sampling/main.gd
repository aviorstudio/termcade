extends Node2D
var camera: Camera2D

func _ready() -> void:
	var image := Image.create(1024, 512, false, Image.FORMAT_RGB8)
	for y in 512:
		for x in 1024:
			image.set_pixel(x, y, Color.WHITE if ((x >> 1) + (y >> 2)) % 2 == 0 else Color.BLACK)
	var sprite := Sprite2D.new()
	sprite.texture = ImageTexture.create_from_image(image)
	sprite.texture_filter = CanvasItem.TEXTURE_FILTER_NEAREST
	sprite.centered = false
	add_child(sprite)
	camera = Camera2D.new()
	camera.position = Vector2(256, 128)
	add_child(camera)

func _physics_process(_delta: float) -> void:
	camera.position += Vector2(1, 1)
