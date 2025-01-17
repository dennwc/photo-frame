#version 330 core

layout(location = 0) in vec2 pos;
layout(location = 1) in vec2 texcoord;

out vec2 Texcoord;

void main()
{
	Texcoord = texcoord;
    gl_Position = vec4(pos.x, -pos.y, 0.0, 1.0);
}